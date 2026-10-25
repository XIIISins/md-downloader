// Package pg is the Postgres backend for store.Store.
//
// Several processes may share one database: Claim uses FOR UPDATE SKIP LOCKED
// and a lease, so a job claimed by a daemon that then dies is handed out again
// once the lease expires.
package pg

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/xiiisins/md-downloader/internal/store"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Lease is how long a claimed job stays reserved without an Update.
const Lease = 10 * time.Minute

// retryFor bounds how long an operation retries through a failover
// (Patroni promotes a new leader within 10-30 s).
const retryFor = 60 * time.Second

var (
	_ store.Store   = (*Store)(nil)
	_ store.Library = (*Store)(nil)
)

// Store is a store.Store backed by Postgres.
type Store struct {
	pool  *pgxpool.Pool
	owner string
}

// Open connects to Postgres, applies pending migrations and returns a Store.
// owner identifies this process in lease bookkeeping.
func Open(ctx context.Context, url, owner string) (*Store, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, err
	}
	s := &Store{pool: pool, owner: owner}
	if err := s.migrate(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("migrating: %w", err)
	}
	return s, nil
}

// Close releases the connection pool.
func (s *Store) Close() { s.pool.Close() }

func (s *Store) migrate(ctx context.Context) error {
	return retry(ctx, func() error {
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return err
		}
		defer tx.Rollback(ctx)
		// Serialise concurrent starters (api, sync and daemon pods).
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(724501)`); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (name text PRIMARY KEY, applied timestamptz NOT NULL DEFAULT now())`); err != nil {
			return err
		}
		entries, err := fs.ReadDir(migrations, "migrations")
		if err != nil {
			return err
		}
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		sort.Strings(names)
		for _, name := range names {
			var done bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE name = $1)`, name).Scan(&done); err != nil {
				return err
			}
			if done {
				continue
			}
			sql, err := migrations.ReadFile("migrations/" + name)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, string(sql)); err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
			if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (name) VALUES ($1)`, name); err != nil {
				return err
			}
		}
		return tx.Commit(ctx)
	})
}

// retry runs fn again on connection-level errors until retryFor elapses.
// Errors reported by the server (constraint violations, bad SQL) are final.
func retry(ctx context.Context, fn func() error) error {
	deadline := time.Now().Add(retryFor)
	delay := 500 * time.Millisecond
	for {
		err := fn()
		var pgErr *pgconn.PgError
		if err == nil || errors.As(err, &pgErr) || errors.Is(err, pgx.ErrNoRows) || errors.Is(err, store.ErrNotRunning) ||
			ctx.Err() != nil || time.Now().After(deadline) {
			return err
		}
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return ctx.Err()
		}
		delay = min(delay*2, 5*time.Second)
	}
}

func (s *Store) Enqueue(ctx context.Context, items []store.Item) (added int, skipped []string, err error) {
	err = retry(ctx, func() error {
		added, skipped = 0, nil
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return err
		}
		defer tx.Rollback(ctx)
		for _, it := range items {
			// A new row, or a finished one re-entering at the back; active jobs are untouched.
			tag, err := tx.Exec(ctx, `
				INSERT INTO jobs (series_id, title, state)
				VALUES ($1, $2, 'queued')
				ON CONFLICT (series_id) DO UPDATE
				   SET title = EXCLUDED.title, state = 'queued', detail = '',
				       seq = DEFAULT, added = now(), updated = now(),
				       lease_owner = NULL, lease_until = NULL
				 WHERE jobs.state NOT IN ('queued', 'running')`, it.ID, it.Title)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				skipped = append(skipped, it.ID)
			} else {
				added++
			}
		}
		return tx.Commit(ctx)
	})
	return added, skipped, err
}

func (s *Store) Cancel(ctx context.Context, id string) (ok bool, err error) {
	err = retry(ctx, func() error {
		tag, err := s.pool.Exec(ctx, `
			UPDATE jobs SET state = 'canceled', detail = 'canceled', updated = now(),
			       lease_owner = NULL, lease_until = NULL
			 WHERE series_id = $1 AND state IN ('queued', 'running')`, id)
		ok = tag.RowsAffected() > 0
		return err
	})
	return ok, err
}

func (s *Store) ClearFinished(ctx context.Context) error {
	return retry(ctx, func() error {
		_, err := s.pool.Exec(ctx, `DELETE FROM jobs WHERE state NOT IN ('queued', 'running')`)
		return err
	})
}

func (s *Store) List(ctx context.Context) (jobs []store.Job, err error) {
	err = retry(ctx, func() error {
		jobs = nil
		rows, err := s.pool.Query(ctx, `SELECT series_id, title, state, detail, added, updated FROM jobs ORDER BY seq`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var j store.Job
			if err := rows.Scan(&j.ID, &j.Title, &j.State, &j.Detail, &j.Added, &j.Updated); err != nil {
				return err
			}
			jobs = append(jobs, j)
		}
		return rows.Err()
	})
	return jobs, err
}

func (s *Store) Claim(ctx context.Context) (job *store.Job, err error) {
	err = retry(ctx, func() error {
		job = nil
		var j store.Job
		// Queued jobs, plus running ones whose owner stopped renewing the lease.
		err := s.pool.QueryRow(ctx, `
			UPDATE jobs SET state = 'running', detail = 'starting', updated = now(),
			       lease_owner = $1, lease_until = now() + $2::interval
			 WHERE series_id = (
			       SELECT series_id FROM jobs
			        WHERE state = 'queued' OR (state = 'running' AND lease_until < now())
			        ORDER BY seq
			        FOR UPDATE SKIP LOCKED
			        LIMIT 1)
			RETURNING series_id, title, state, detail, added, updated`,
			s.owner, fmt.Sprintf("%d seconds", int(Lease.Seconds()))).
			Scan(&j.ID, &j.Title, &j.State, &j.Detail, &j.Added, &j.Updated)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err == nil {
			job = &j
		}
		return err
	})
	return job, err
}

// Update sets the detail line and renews the lease.
func (s *Store) Update(ctx context.Context, id, detail string) error {
	return retry(ctx, func() error {
		tag, err := s.pool.Exec(ctx, `
			UPDATE jobs SET detail = $2, updated = now(), lease_until = now() + $4::interval
			 WHERE series_id = $1 AND state = 'running' AND lease_owner = $3`,
			id, detail, s.owner, fmt.Sprintf("%d seconds", int(Lease.Seconds())))
		if err == nil && tag.RowsAffected() == 0 {
			return store.ErrNotRunning
		}
		return err
	})
}

func (s *Store) Finish(ctx context.Context, id, state, detail string) error {
	return retry(ctx, func() error {
		_, err := s.pool.Exec(ctx, `
			UPDATE jobs SET state = $2, detail = $3, updated = now(),
			       lease_owner = NULL, lease_until = NULL
			 WHERE series_id = $1 AND (lease_owner = $4 OR lease_owner IS NULL)`, id, state, detail, s.owner)
		return err
	})
}
