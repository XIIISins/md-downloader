package pg

import (
	"context"

	"github.com/xiiisins/md-downloader/internal/store"
)

var _ store.Progress = (*Store)(nil)

// SetExpected implements store.Progress.
func (s *Store) SetExpected(ctx context.Context, items []store.Expected) error {
	ids := make([]string, len(items))
	for i, it := range items {
		ids[i] = it.ID
	}
	return retry(ctx, func() error {
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return err
		}
		defer tx.Rollback(ctx)
		if _, err := tx.Exec(ctx, `DELETE FROM library_chapters WHERE NOT (series_id = ANY($1))`, ids); err != nil {
			return err
		}
		for _, it := range items {
			keys := it.Keys
			if keys == nil {
				keys = []string{}
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO library_chapters (series_id, title, keys) VALUES ($1, $2, $3)
				ON CONFLICT (series_id) DO UPDATE SET title = EXCLUDED.title, keys = EXCLUDED.keys`,
				it.ID, it.Title, keys); err != nil {
				return err
			}
		}
		return tx.Commit(ctx)
	})
}

// Expected implements store.Progress.
func (s *Store) Expected(ctx context.Context) (out []store.Expected, err error) {
	err = retry(ctx, func() error {
		out = nil
		rows, err := s.pool.Query(ctx, `SELECT series_id, title, keys, downloaded FROM library_chapters ORDER BY series_id`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var e store.Expected
			if err := rows.Scan(&e.ID, &e.Title, &e.Keys, &e.Downloaded); err != nil {
				return err
			}
			out = append(out, e)
		}
		return rows.Err()
	})
	return out, err
}

// SetDownloaded implements store.Progress.
func (s *Store) SetDownloaded(ctx context.Context, id string, n int) error {
	return retry(ctx, func() error {
		_, err := s.pool.Exec(ctx, `UPDATE library_chapters SET downloaded = $2 WHERE series_id = $1`, id, n)
		return err
	})
}

// Downloaded implements store.Progress.
func (s *Store) Downloaded(ctx context.Context) (out map[string]int, err error) {
	err = retry(ctx, func() error {
		out = map[string]int{}
		rows, err := s.pool.Query(ctx, `SELECT series_id, downloaded FROM library_chapters`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			var n int
			if err := rows.Scan(&id, &n); err != nil {
				return err
			}
			out[id] = n
		}
		return rows.Err()
	})
	return out, err
}
