// Package download fetches chapters from MangaDex and writes them to disk as
// CBZ files (or plain image folders).
package download

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/xiiisins/md-downloader/internal/mangadex"
	"github.com/xiiisins/md-downloader/internal/retry"
)

const (
	maxRetries = 4

	// pageRounds is the initial pass over a chapter plus retries of the pages
	// that failed. Each retry asks for a fresh image server, as MangaDex
	// recommends when a node is slow or failing.
	pageRounds = 3
)

// Downloader downloads chapters through a MangaDex client.
type Downloader struct {
	API      *mangadex.Client
	DataSave bool // fetch the compressed "data-saver" images
	Workers  int  // concurrent page downloads per chapter
}

// downloadChapter fetches all pages of ch into dir. Returns number of pages.
func (d *Downloader) downloadChapter(ctx context.Context, ch mangadex.Chapter, dir string) (int, error) {
	var (
		files   []string
		pending []int
		errs    map[int]error
	)
	for round := 0; round < pageRounds; round++ {
		ah, err := d.API.AtHomeServer(ctx, ch.ID)
		if err != nil {
			if round == 0 {
				return 0, err
			}
			break // keep what we have and report the pages still missing
		}
		fs, quality := ah.Chapter.Data, "data"
		if d.DataSave {
			fs, quality = ah.Chapter.DataSaver, "data-saver"
		}
		if round == 0 {
			if len(fs) == 0 {
				return 0, errors.New("no pages returned")
			}
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return 0, err
			}
			files = fs
			for i := range files {
				pending = append(pending, i)
			}
		} else if len(fs) != len(files) {
			break // the chapter changed under us; don't mix versions
		}

		report := !strings.HasSuffix(hostOf(ah.BaseURL), "mangadex.org")
		errs = d.fetchPages(ctx, ah.BaseURL, quality, ah.Chapter.Hash, fs, pending, dir, report)
		pending = pending[:0]
		for i := range files {
			if _, failed := errs[i]; failed {
				pending = append(pending, i)
			}
		}
		if len(pending) == 0 || ctx.Err() != nil {
			break
		}
		retry.Sleep(ctx, 2*retry.Unit)
	}

	if len(pending) > 0 {
		return len(files) - len(pending), fmt.Errorf("%d page(s) failed: page %d: %w", len(pending), pending[0]+1, errs[pending[0]])
	}
	return len(files), nil
}

// fetchPages downloads the pages at the given indices with d.Workers workers and
// returns the error for each page that failed. Pages already on disk are skipped.
func (d *Downloader) fetchPages(ctx context.Context, base, quality, hash string, files []string, idx []int, dir string, report bool) map[int]error {
	jobs := make(chan int)
	var (
		mu     sync.Mutex
		failed = map[int]error{}
		wg     sync.WaitGroup
	)
	for w := 0; w < d.Workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				dst := filepath.Join(dir, fmt.Sprintf("%03d%s", i+1, filepath.Ext(files[i])))
				if st, err := os.Stat(dst); err == nil && st.Size() > 0 {
					continue // resume
				}
				u := fmt.Sprintf("%s/%s/%s/%s", base, quality, hash, files[i])
				if err := d.downloadPage(ctx, u, dst, report); err != nil {
					mu.Lock()
					failed[i] = err
					mu.Unlock()
				}
			}
		}()
	}
	for _, i := range idx {
		select {
		case jobs <- i:
		case <-ctx.Done():
		}
	}
	close(jobs)
	wg.Wait()
	return failed
}

func (d *Downloader) downloadPage(ctx context.Context, u, dst string, report bool) error {
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		start := time.Now()
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		req.Header.Set("User-Agent", mangadex.UserAgent)
		resp, err := d.API.HTTP.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err() // canceled by us; not a server failure, so don't report it
			}
			lastErr = err
			if report {
				d.API.Report(u, false, 0, time.Since(start), false)
			}
			retry.Sleep(ctx, retry.Backoff(attempt))
			continue
		}
		body, rerr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if rerr != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		elapsed := time.Since(start)
		cached := strings.HasPrefix(strings.ToUpper(resp.Header.Get("X-Cache")), "HIT")
		ok := rerr == nil && resp.StatusCode == http.StatusOK && len(body) > 0
		if report {
			d.API.Report(u, ok, len(body), elapsed, cached)
		}
		if !ok {
			lastErr = fmt.Errorf("HTTP %d (read err: %v)", resp.StatusCode, rerr)
			retry.Sleep(ctx, retry.Backoff(attempt))
			continue
		}
		tmp := dst + ".part"
		if err := os.WriteFile(tmp, body, 0o644); err != nil {
			return err
		}
		return os.Rename(tmp, dst)
	}
	return lastErr
}

func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Hostname()
}
