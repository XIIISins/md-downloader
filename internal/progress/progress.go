// Package progress works out how much of each library title is on disk. Only
// the daemon can see the output directory, so it runs the scan and records the
// counts in the shared store for the API to serve.
package progress

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/xiiisins/md-downloader/internal/download"
	"github.com/xiiisins/md-downloader/internal/store"
)

// Interval is how often the daemon rescans, to notice changes made outside the
// app such as deleted files.
const Interval = 15 * time.Minute

// checkOut fails with download.ErrOutputUnavailable when out can't be trusted
// as a listing: it is missing, not a directory, or empty. An unmounted share
// leaves an empty mount point behind, and counting that would zero every title.
func checkOut(out string) error {
	st, err := os.Stat(out)
	if err != nil || !st.IsDir() {
		return fmt.Errorf("%w: %s", download.ErrOutputUnavailable, out)
	}
	ents, err := os.ReadDir(out)
	if err != nil {
		return fmt.Errorf("%w: %s: %v", download.ErrOutputUnavailable, out, err)
	}
	if len(ents) == 0 {
		return fmt.Errorf("%w: %s is empty", download.ErrOutputUnavailable, out)
	}
	return nil
}

// Count returns how many of keys are on disk for the series called title under
// out. It only lists the series folder, never reads file contents. In "cbz"
// format a key is present as a file ch<key>.cbz, in "dir" format as a folder
// ch<key>. Other entries, such as stray files, are ignored, so they can't hide a
// missing chapter. A series with no folder has none.
func Count(out, format, title string, keys []string) (int, error) {
	ents, err := os.ReadDir(filepath.Join(out, download.DirName(title)))
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	have := make(map[string]bool, len(ents))
	for _, e := range ents {
		name := e.Name()
		switch {
		case format == "dir" && e.IsDir() && strings.HasPrefix(name, "ch"):
			have[name[2:]] = true
		case format != "dir" && !e.IsDir() && strings.HasPrefix(name, "ch") && strings.HasSuffix(name, ".cbz"):
			have[strings.TrimSuffix(name[2:], ".cbz")] = true
		}
	}
	n := 0
	seen := make(map[string]bool, len(keys))
	for _, k := range keys {
		if !seen[k] && have[k] {
			n++
		}
		seen[k] = true
	}
	return n, nil
}

// Scanner records how much of each series is on disk.
type Scanner struct {
	Store  store.Progress
	Out    string
	Format string // "cbz" or "dir"
}

// ScanAll rescans every series. If the output directory is unavailable it
// returns that error and leaves the stored counts as they were.
func (s *Scanner) ScanAll(ctx context.Context) error {
	exp, err := s.Store.Expected(ctx)
	if err != nil {
		return err
	}
	return s.scan(ctx, exp)
}

// ScanOne rescans one series, for use right after its download finished. A
// series the sync worker doesn't know yet has nothing to compare and is skipped.
func (s *Scanner) ScanOne(ctx context.Context, id string) error {
	exp, err := s.Store.Expected(ctx)
	if err != nil {
		return err
	}
	for _, e := range exp {
		if e.ID == id {
			return s.scan(ctx, []store.Expected{e})
		}
	}
	return nil
}

func (s *Scanner) scan(ctx context.Context, exp []store.Expected) error {
	if err := checkOut(s.Out); err != nil {
		return err
	}
	for _, e := range exp {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		n, err := Count(s.Out, s.Format, e.Title, e.Keys)
		if err != nil {
			log.Printf("scanning %q: %v", e.Title, err)
			continue // keep its last count
		}
		if n == e.Downloaded {
			continue
		}
		if err := s.Store.SetDownloaded(ctx, e.ID, n); err != nil {
			return err
		}
	}
	return nil
}

// Run scans now and then every Interval until ctx is done.
func (s *Scanner) Run(ctx context.Context) {
	t := time.NewTicker(Interval)
	defer t.Stop()
	for {
		switch err := s.ScanAll(ctx); {
		case err == nil, ctx.Err() != nil:
		case errors.Is(err, download.ErrOutputUnavailable):
			log.Printf("progress scan skipped, keeping the last counts: %v", err)
		default:
			log.Printf("progress scan: %v", err)
		}
		select {
		case <-t.C:
		case <-ctx.Done():
			return
		}
	}
}
