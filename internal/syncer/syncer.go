// Package syncer periodically copies the user's MangaDex library into the
// shared database. It is the only component that talks to MangaDex for library
// metadata, so the API and UI stay stateless.
package syncer

import (
	"context"
	"log"
	"time"

	"github.com/xiiisins/md-downloader/internal/library"
	"github.com/xiiisins/md-downloader/internal/mangadex"
	"github.com/xiiisins/md-downloader/internal/retry"
	"github.com/xiiisins/md-downloader/internal/store"
)

// Config describes a sync worker.
type Config struct {
	Client   *mangadex.Client // must be logged in
	Library  store.Library
	Progress store.Progress    // optional; receives each title's expected chapter files
	Cache    library.Persister // survives restarts so a sync only fetches what is stale
	Status   string
	Lang     string
	Ratings  []string
	Interval time.Duration
	Once     bool // sync once and return
	Refresh  bool // ignore cached metadata on the first run
}

// sync runs a single sync.
func (c Config) sync(ctx context.Context, refresh bool) error {
	ids, err := c.Client.LibraryStatuses(ctx, c.Status)
	if err != nil {
		return err
	}
	mc := library.LoadCache(c.Cache)
	items, err := library.LoadItems(ctx, c.Client, ids, c.Lang, c.Ratings, mc, refresh)
	if err != nil {
		return err
	}
	if err := c.Library.ReplaceLibrary(ctx, items); err != nil {
		return err
	}
	if c.Progress != nil {
		exp := make([]store.Expected, len(items))
		for i, it := range items {
			exp[i] = store.Expected{ID: it.ID, Title: it.Title, Keys: mc.ChapterKeys(it.ID)}
		}
		if err := c.Progress.SetExpected(ctx, exp); err != nil {
			return err
		}
	}
	log.Printf("Synced %d title(s)", len(items))
	return nil
}

// Run syncs now and then every Interval until ctx is done. A failed sync is
// retried after a short delay rather than waiting out the interval.
func Run(ctx context.Context, c Config) error {
	refresh := c.Refresh
	for {
		err := c.sync(ctx, refresh)
		if c.Once {
			return err
		}
		wait := c.Interval
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			log.Printf("sync failed: %v", err)
			wait = min(wait, 5*time.Minute)
		} else {
			refresh = false
		}
		retry.Sleep(ctx, wait)
		if ctx.Err() != nil {
			return nil
		}
	}
}
