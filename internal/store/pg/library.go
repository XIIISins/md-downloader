package pg

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/xiiisins/md-downloader/internal/mangadex"
)

// ReplaceLibrary makes items the whole library listing.
func (s *Store) ReplaceLibrary(ctx context.Context, items []mangadex.Manga) error {
	ids := make([]string, len(items))
	rows := make([][]byte, len(items))
	for i, it := range items {
		b, err := json.Marshal(it)
		if err != nil {
			return err
		}
		ids[i], rows[i] = it.ID, b
	}
	return retry(ctx, func() error {
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return err
		}
		defer tx.Rollback(ctx)
		if _, err := tx.Exec(ctx, `DELETE FROM library_items WHERE NOT (series_id = ANY($1))`, ids); err != nil {
			return err
		}
		for i, it := range items {
			if _, err := tx.Exec(ctx, `
				INSERT INTO library_items (series_id, data, cover, synced) VALUES ($1, $2, $3, now())
				ON CONFLICT (series_id) DO UPDATE SET data = EXCLUDED.data, cover = EXCLUDED.cover, synced = now()`,
				it.ID, rows[i], it.Cover); err != nil {
				return err
			}
		}
		return tx.Commit(ctx)
	})
}

// Library returns the synced listing, sorted by title, and when it was last synced.
func (s *Store) Library(ctx context.Context) (items []mangadex.Manga, synced time.Time, err error) {
	err = retry(ctx, func() error {
		items, synced = nil, time.Time{}
		rows, err := s.pool.Query(ctx, `SELECT data, cover, synced FROM library_items`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var (
				b     []byte
				cover string
				at    time.Time
			)
			if err := rows.Scan(&b, &cover, &at); err != nil {
				return err
			}
			var it mangadex.Manga
			if err := json.Unmarshal(b, &it); err != nil {
				return err
			}
			it.Cover = cover
			items = append(items, it)
			if at.After(synced) {
				synced = at
			}
		}
		return rows.Err()
	})
	sort.Slice(items, func(i, j int) bool {
		return strings.ToLower(items[i].Title) < strings.ToLower(items[j].Title)
	})
	return items, synced, err
}

// Blob is a named blob in the database, usable as a library.Persister.
type Blob struct {
	s    *Store
	name string
}

// Blob returns the blob called name.
func (s *Store) Blob(name string) *Blob { return &Blob{s, name} }

// Read returns the blob, or nil if it was never written.
func (b *Blob) Read() ([]byte, error) {
	var data []byte
	err := retry(context.Background(), func() error {
		return b.s.pool.QueryRow(context.Background(), `SELECT data FROM blobs WHERE name = $1`, b.name).Scan(&data)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return data, err
}

// Write replaces the blob.
func (b *Blob) Write(data []byte) error {
	return retry(context.Background(), func() error {
		_, err := b.s.pool.Exec(context.Background(), `
			INSERT INTO blobs (name, data) VALUES ($1, $2)
			ON CONFLICT (name) DO UPDATE SET data = EXCLUDED.data, updated = now()`, b.name, data)
		return err
	})
}
