// mangadex-scraper downloads chapters from MangaDex for personal offline reading.
//
// It follows the documented MangaDex flow: list chapters via the feed endpoint,
// resolve an image server via /at-home/server, download pages, and report each
// download to the MangaDex network so they can monitor server health.
package main

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	reportURL   = "https://api.mangadex.network/report"
	userAgent   = "mangadex-scraper/1.0 (personal offline reader)"
	maxRetries  = 4
	reportPause = 10 * time.Minute
)

// apiBase is a variable so tests can point the client at a fake server.
var apiBase = "https://api.mangadex.org"

var uuidRe = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)

// limiter spaces calls at least `interval` apart.
type limiter struct {
	mu       sync.Mutex
	interval time.Duration
	next     time.Time
}

func (l *limiter) wait(ctx context.Context) error {
	l.mu.Lock()
	now := time.Now()
	if l.next.Before(now) {
		l.next = now
	}
	at := l.next
	l.next = at.Add(l.interval)
	l.mu.Unlock()

	select {
	case <-time.After(time.Until(at)):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type client struct {
	http   *http.Client
	api    *limiter     // general API: ~5 req/s, stay under with 250ms
	atHome *limiter     // /at-home/server: ~40 req/min, stay under with 1.6s
	auth   *authSession // nil unless logged in

	reportSem   chan struct{} // bounds in-flight download reports
	reportFails atomic.Int32  // consecutive report failures
	reportOff   atomic.Int64  // unix nanos until which reporting is paused
	dataSave    bool
	workers     int
}

// getJSON performs a rate-limited GET against the API, honouring 429s.
func (c *client) getJSON(ctx context.Context, lim *limiter, u string, out any) error {
	return c.doJSON(ctx, lim, u, out, false)
}

// getJSONAuth is getJSON with the user's bearer token attached.
func (c *client) getJSONAuth(ctx context.Context, lim *limiter, u string, out any) error {
	if c.auth == nil {
		return errors.New("not logged in")
	}
	return c.doJSON(ctx, lim, u, out, true)
}

func (c *client) doJSON(ctx context.Context, lim *limiter, u string, out any, bearer bool) error {
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if err := lim.wait(ctx); err != nil {
			return err
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		req.Header.Set("User-Agent", userAgent)
		if bearer {
			tok, err := c.auth.token(ctx)
			if err != nil {
				return err
			}
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		resp, err := c.http.Do(req)
		if err != nil {
			lastErr = err
			sleep(ctx, backoff(attempt))
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		switch {
		case resp.StatusCode == http.StatusTooManyRequests:
			d := 5 * time.Second
			if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil {
				d = time.Duration(s) * time.Second
			}
			lastErr = errors.New("rate limited")
			sleep(ctx, d)
		case resp.StatusCode == http.StatusUnauthorized && bearer:
			lastErr = errors.New("unauthorized")
			c.auth.invalidate()
		case resp.StatusCode >= 500:
			lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
			sleep(ctx, backoff(attempt))
		case resp.StatusCode != http.StatusOK:
			return fmt.Errorf("GET %s: HTTP %d: %s", u, resp.StatusCode, truncate(body, 200))
		default:
			return json.Unmarshal(body, out)
		}
	}
	return fmt.Errorf("GET %s: %w", u, lastErr)
}

type chapter struct {
	ID      string
	Volume  string
	Number  string
	Title   string
	Pages   int
	Ext     string // externally hosted; not downloadable
	Updated string
}

// downloadable reports whether the chapter's pages are hosted by MangaDex.
func (ch chapter) downloadable() bool { return ch.Ext == "" && ch.Pages > 0 }

func (c *client) mangaTitle(ctx context.Context, id string) (string, error) {
	var r struct {
		Data struct {
			Attributes struct {
				Title map[string]string `json:"title"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if err := c.getJSON(ctx, c.api, apiBase+"/manga/"+id, &r); err != nil {
		return "", err
	}
	t := r.Data.Attributes.Title
	if v := t["en"]; v != "" {
		return v, nil
	}
	for _, v := range t {
		return v, nil
	}
	return id, nil
}

func (c *client) chapters(ctx context.Context, id, lang string, ratings []string) ([]chapter, error) {
	var all []chapter
	seen := map[string]int{} // chapter key -> index in all
	for offset := 0; ; {
		q := url.Values{}
		q.Set("limit", "500")
		q.Set("offset", strconv.Itoa(offset))
		q.Add("translatedLanguage[]", lang)
		q.Set("order[volume]", "asc")
		q.Set("order[chapter]", "asc")
		for _, r := range ratings {
			q.Add("contentRating[]", r)
		}
		var r struct {
			Total int `json:"total"`
			Data  []struct {
				ID         string `json:"id"`
				Attributes struct {
					Volume      *string `json:"volume"`
					Chapter     *string `json:"chapter"`
					Title       *string `json:"title"`
					Pages       int     `json:"pages"`
					ExternalURL *string `json:"externalUrl"`
				} `json:"attributes"`
			} `json:"data"`
		}
		if err := c.getJSON(ctx, c.api, fmt.Sprintf("%s/manga/%s/feed?%s", apiBase, id, q.Encode()), &r); err != nil {
			return nil, err
		}
		for _, d := range r.Data {
			a := d.Attributes
			ch := chapter{ID: d.ID, Pages: a.Pages, Number: "oneshot"}
			if a.Chapter != nil && *a.Chapter != "" {
				ch.Number = *a.Chapter
			}
			if a.Volume != nil {
				ch.Volume = *a.Volume
			}
			if a.Title != nil {
				ch.Title = *a.Title
			}
			if a.ExternalURL != nil {
				ch.Ext = *a.ExternalURL
			}
			// Several groups may upload the same chapter. Keep the first, unless it
			// can't be downloaded (external link or no pages) and a later one can.
			key := ch.Volume + "/" + ch.Number
			if i, dup := seen[key]; dup && ch.Number != "oneshot" {
				if !all[i].downloadable() && ch.downloadable() {
					all[i] = ch
				}
				continue
			}
			seen[key] = len(all)
			all = append(all, ch)
		}
		offset += len(r.Data)
		if len(r.Data) == 0 || offset >= r.Total {
			break
		}
	}
	return all, nil
}

type atHome struct {
	BaseURL string `json:"baseUrl"`
	Chapter struct {
		Hash      string   `json:"hash"`
		Data      []string `json:"data"`
		DataSaver []string `json:"dataSaver"`
	} `json:"chapter"`
}

// downloadChapter fetches all pages of ch into dir. Returns number of pages.
// pageRounds is the initial pass over a chapter plus retries of the pages that
// failed. Each retry asks for a fresh image server, as MangaDex recommends when
// a node is slow or failing.
const pageRounds = 3

func (c *client) downloadChapter(ctx context.Context, ch chapter, dir string) (int, error) {
	var (
		files   []string
		pending []int
		errs    map[int]error
	)
	for round := 0; round < pageRounds; round++ {
		var ah atHome
		if err := c.getJSON(ctx, c.atHome, apiBase+"/at-home/server/"+ch.ID, &ah); err != nil {
			if round == 0 {
				return 0, err
			}
			break // keep what we have and report the pages still missing
		}
		fs, quality := ah.Chapter.Data, "data"
		if c.dataSave {
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
		errs = c.fetchPages(ctx, ah.BaseURL, quality, ah.Chapter.Hash, fs, pending, dir, report)
		pending = pending[:0]
		for i := range files {
			if _, failed := errs[i]; failed {
				pending = append(pending, i)
			}
		}
		if len(pending) == 0 || ctx.Err() != nil {
			break
		}
		sleep(ctx, 2*backoffUnit)
	}

	if len(pending) > 0 {
		return len(files) - len(pending), fmt.Errorf("%d page(s) failed: page %d: %w", len(pending), pending[0]+1, errs[pending[0]])
	}
	return len(files), nil
}

// fetchPages downloads the pages at the given indices with c.workers workers and
// returns the error for each page that failed. Pages already on disk are skipped.
func (c *client) fetchPages(ctx context.Context, base, quality, hash string, files []string, idx []int, dir string, report bool) map[int]error {
	jobs := make(chan int)
	var (
		mu     sync.Mutex
		failed = map[int]error{}
		wg     sync.WaitGroup
	)
	for w := 0; w < c.workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				dst := filepath.Join(dir, fmt.Sprintf("%03d%s", i+1, filepath.Ext(files[i])))
				if st, err := os.Stat(dst); err == nil && st.Size() > 0 {
					continue // resume
				}
				u := fmt.Sprintf("%s/%s/%s/%s", base, quality, hash, files[i])
				if err := c.downloadPage(ctx, u, dst, report); err != nil {
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

func (c *client) downloadPage(ctx context.Context, u, dst string, report bool) error {
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		start := time.Now()
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		req.Header.Set("User-Agent", userAgent)
		resp, err := c.http.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err() // canceled by us; not a server failure, so don't report it
			}
			lastErr = err
			if report {
				c.report(u, false, 0, time.Since(start), false)
			}
			sleep(ctx, backoff(attempt))
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
			c.report(u, ok, len(body), elapsed, cached)
		}
		if !ok {
			lastErr = fmt.Errorf("HTTP %d (read err: %v)", resp.StatusCode, rerr)
			sleep(ctx, backoff(attempt))
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

// report tells the MangaDex network about a download result. It is best effort
// and runs in the background so a slow or dead endpoint never stalls downloads.
// After a few consecutive failures it pauses reporting for reportPause.
func (c *client) report(u string, success bool, bytes int, d time.Duration, cached bool) {
	if time.Now().UnixNano() < c.reportOff.Load() {
		return
	}
	select {
	case c.reportSem <- struct{}{}:
	default:
		return // too many reports in flight; drop this one
	}
	payload, _ := json.Marshal(map[string]any{
		"url":      u,
		"success":  success,
		"bytes":    bytes,
		"duration": d.Milliseconds(),
		"cached":   cached,
	})
	go func() {
		defer func() { <-c.reportSem }()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, reportURL, bytesReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", userAgent)
		resp, err := c.http.Do(req)
		if err == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
		if err != nil || resp.StatusCode >= 500 {
			if c.reportFails.Add(1) >= 3 {
				c.reportFails.Store(0)
				c.reportOff.Store(time.Now().Add(reportPause).UnixNano())
				log.Printf("MangaDex report endpoint is failing; pausing reports for %s", reportPause)
			}
			return
		}
		c.reportFails.Store(0)
	}()
}

func zipDir(dir, dst string) error {
	f, err := os.Create(dst + ".part")
	if err != nil {
		return err
	}
	zw := zip.NewWriter(f)
	entries, err := os.ReadDir(dir)
	if err != nil {
		f.Close()
		return err
	}
	for _, e := range entries {
		if e.IsDir() || strings.HasSuffix(e.Name(), ".part") {
			continue
		}
		// Images are already compressed; store them.
		w, err := zw.CreateHeader(&zip.FileHeader{Name: e.Name(), Method: zip.Store})
		if err != nil {
			f.Close()
			return err
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			f.Close()
			return err
		}
		if _, err := w.Write(b); err != nil {
			f.Close()
			return err
		}
	}
	if err := zw.Close(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(dst+".part", dst)
}

var badChars = regexp.MustCompile(`[<>:"/\\|?*\x00-\x1f]`)

func sanitize(s string) string {
	s = strings.TrimSpace(badChars.ReplaceAllString(s, "_"))
	s = strings.Trim(s, ".")
	if s == "" {
		return "untitled"
	}
	if len(s) > 120 {
		s = s[:120]
	}
	return s
}

// padNumber zero-pads the integer part so folders sort correctly: "5.5" -> "0005.5".
func padNumber(n string) string {
	intPart, frac, hasFrac := strings.Cut(n, ".")
	if _, err := strconv.Atoi(intPart); err != nil {
		return n
	}
	intPart = strings.Repeat("0", max(0, 4-len(intPart))) + intPart
	if hasFrac {
		return intPart + "." + frac
	}
	return intPart
}

// errOutputUnavailable means the output directory is missing, typically because
// a NAS mount dropped. Callers should wait and retry rather than fail the series.
var errOutputUnavailable = errors.New("output directory unavailable")

// ensureOutDir checks that the output directory exists, creating only its last
// path element. If a parent (such as a NAS mount point) is missing it fails
// instead of creating a local directory that would shadow the mount.
func ensureOutDir(out string) error {
	if st, err := os.Stat(out); err == nil {
		if st.IsDir() {
			return nil
		}
		return fmt.Errorf("%w: %s is not a directory", errOutputUnavailable, out)
	}
	if err := os.Mkdir(out, 0o755); err != nil {
		return fmt.Errorf("%w: %s: %v", errOutputUnavailable, out, err)
	}
	return nil
}

type options struct {
	lang    string
	out     string
	format  string
	ratings []string
	from    float64
	to      float64
}

// tally counts what happened to a series' chapters. have is chapters already on
// disk; external is chapters MangaDex doesn't host (official links or no pages).
type tally struct{ ok, have, external, failed int }

func (t tally) String() string {
	s := fmt.Sprintf("%d downloaded, %d already had, %d external/unavailable", t.ok, t.have, t.external)
	if t.failed > 0 {
		s += fmt.Sprintf(", %d failed", t.failed)
	}
	return s
}

// syncManga downloads the selected chapters of one manga.
func (c *client) syncManga(ctx context.Context, id string, o options, logf func(string, ...any)) (tally, error) {
	var t tally
	if err := ensureOutDir(o.out); err != nil {
		return t, err
	}
	title, err := c.mangaTitle(ctx, id)
	if err != nil {
		return t, fmt.Errorf("fetching manga: %w", err)
	}
	logf("Manga: %s", title)

	chs, err := c.chapters(ctx, id, o.lang, o.ratings)
	if err != nil {
		return t, fmt.Errorf("fetching chapters: %w", err)
	}
	logf("Found %d chapter(s) in %q", len(chs), o.lang)

	mangaDir := filepath.Join(o.out, sanitize(title))
	for i, ch := range chs {
		if ctx.Err() != nil {
			break
		}
		if err := ensureOutDir(o.out); err != nil {
			logf("stopping: %v", err)
			return t, err
		}
		if n, err := strconv.ParseFloat(ch.Number, 64); err == nil {
			if (o.from >= 0 && n < o.from) || (o.to >= 0 && n > o.to) {
				continue
			}
		}
		if !ch.downloadable() {
			logf("[%d/%d] Ch. %s: externally hosted or empty, skipping", i+1, len(chs), ch.Number)
			t.external++
			continue
		}

		name := "ch" + padNumber(ch.Number)
		dir := filepath.Join(mangaDir, name)
		cbz := dir + ".cbz"

		if o.format == "cbz" {
			if _, err := os.Stat(cbz); err == nil {
				t.have++
				continue
			}
		}

		logf("[%d/%d] %s (%d pages)", i+1, len(chs), name, ch.Pages)
		n, err := c.downloadChapter(ctx, ch, dir)
		if err != nil {
			logf("  failed (%d ok): %v", n, err)
			t.failed++
			continue
		}
		if o.format == "cbz" {
			if err := zipDir(dir, cbz); err != nil {
				logf("  zip failed: %v", err)
				t.failed++
				continue
			}
			os.RemoveAll(dir)
		}
		t.ok++
	}
	logf("Done: %s -> %s", t, mangaDir)
	return t, nil
}

func main() {
	var (
		target     = flag.String("manga", "", "MangaDex title URL or UUID")
		library    = flag.Bool("library", false, "pick titles from your MangaDex library in a local web UI")
		status     = flag.String("status", "reading", "library status to list: reading, on_hold, plan_to_read, dropped, re_reading, completed, all")
		tokenFile  = flag.String("token-file", "personal-api-token", "file with id:/secret: (and optionally username:/password:) lines")
		addr       = flag.String("addr", "127.0.0.1:8484", "listen address for -library UI")
		cacheDir   = flag.String("cache-dir", defaultCacheDir(), "where -library keeps its metadata and cover cache")
		daemon     = flag.Bool("daemon", false, "run the downloader: works through a persistent queue that -library fills")
		daemonAddr = flag.String("daemon-addr", "127.0.0.1:8485", "address the downloader daemon listens on (and -library connects to)")
		stateDir   = flag.String("state-dir", defaultStateDir(), "where the daemon keeps its queue")
		refresh    = flag.Bool("refresh", false, "ignore cached library data and re-fetch everything")
		lang       = flag.String("lang", "en", "translated language code")
		out        = flag.String("out", "downloads", "output directory")
		format     = flag.String("format", "cbz", "output format: cbz or dir")
		dataSave   = flag.Bool("data-saver", false, "download compressed images")
		workers    = flag.Int("workers", 4, "concurrent page downloads per chapter")
		from       = flag.Float64("from", -1, "first chapter number to download")
		to         = flag.Float64("to", -1, "last chapter number to download")
		ratings    = flag.String("ratings", "safe,suggestive", "content ratings: safe,suggestive,erotica,pornographic")
	)
	flag.Parse()

	id := uuidRe.FindString(*target)
	if id == "" && !*library && !*daemon {
		fmt.Fprintln(os.Stderr, "usage: mangadex-scraper (-manga <url-or-uuid> | -library | -daemon) [flags]")
		flag.PrintDefaults()
		os.Exit(2)
	}
	if *format != "cbz" && *format != "dir" {
		log.Fatalf("invalid -format %q (want cbz or dir)", *format)
	}

	ctx, stop := signalContext()
	defer stop()

	c := &client{
		http:     &http.Client{Timeout: 60 * time.Second},
		api:      &limiter{interval: 250 * time.Millisecond},
		atHome:   &limiter{interval: 1600 * time.Millisecond},
		dataSave: *dataSave,
		workers:  max(1, *workers),

		reportSem: make(chan struct{}, 8),
	}
	opts := options{
		lang:    *lang,
		out:     *out,
		format:  *format,
		ratings: strings.Split(*ratings, ","),
		from:    *from,
		to:      *to,
	}

	if *daemon {
		opts.from, opts.to = -1, -1 // chapter ranges only make sense for a single -manga run
		if err := runDaemon(ctx, c, *daemonAddr, *stateDir, opts); err != nil {
			log.Fatal(err)
		}
		return
	}

	if *library {
		creds, err := loadCredentials(*tokenFile)
		if err != nil {
			log.Fatalf("credentials: %v", err)
		}
		c.auth = &authSession{http: c.http, creds: creds}
		if err := runLibraryUI(ctx, c, *status, *addr, *cacheDir, *daemonAddr, *refresh, opts); err != nil {
			log.Fatal(err)
		}
		return
	}

	t, err := c.syncManga(ctx, id, opts, log.Printf)
	if err != nil {
		log.Fatal(err)
	}
	if t.failed > 0 {
		os.Exit(1)
	}
}
