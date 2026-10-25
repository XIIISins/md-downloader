package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

//go:embed index.html
var indexHTML []byte

type libItem struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
	Status      string   `json:"status"`
	Lang        string   `json:"lang"`
	Demographic string   `json:"demographic"`
	Year        int      `json:"year"`
	Rating      float64  `json:"rating"`
	Follows     int      `json:"follows"`
	cover       string   // cover file name on uploads.mangadex.org
}

// cleanDescription drops the "---" trailer where uploaders put links and credits.
func cleanDescription(m map[string]string) string {
	d := m["en"]
	if d == "" {
		return ""
	}
	if i := strings.Index(d, "\n---"); i >= 0 {
		d = d[:i]
	}
	return strings.TrimSpace(d)
}

func pickTitle(t map[string]string, fallback string) string {
	if v := t["en"]; v != "" {
		return v
	}
	for _, v := range t {
		return v
	}
	return fallback
}

// mangaDetails fetches titles and cover file names, 100 IDs per request.
func (c *client) mangaDetails(ctx context.Context, ids []string, lang string) ([]libItem, error) {
	var items []libItem
	for start := 0; start < len(ids); start += 100 {
		batch := ids[start:min(start+100, len(ids))]
		q := url.Values{}
		q.Set("limit", "100")
		q.Add("includes[]", "cover_art")
		q.Add("availableTranslatedLanguage[]", lang)
		for _, r := range []string{"safe", "suggestive", "erotica", "pornographic"} {
			q.Add("contentRating[]", r)
		}
		for _, id := range batch {
			q.Add("ids[]", id)
		}
		var r struct {
			Data []struct {
				ID         string `json:"id"`
				Attributes struct {
					Title             map[string]string `json:"title"`
					Description       map[string]string `json:"description"`
					Status            string            `json:"status"`
					OriginalLanguage  string            `json:"originalLanguage"`
					PublicDemographic *string           `json:"publicDemographic"`
					Year              *int              `json:"year"`
					Tags              []struct {
						Attributes struct {
							Name map[string]string `json:"name"`
						} `json:"attributes"`
					} `json:"tags"`
				} `json:"attributes"`
				Relationships []struct {
					Type       string `json:"type"`
					Attributes struct {
						FileName string `json:"fileName"`
					} `json:"attributes"`
				} `json:"relationships"`
			} `json:"data"`
		}
		if err := c.getJSON(ctx, c.api, apiBase+"/manga?"+q.Encode(), &r); err != nil {
			return nil, err
		}
		for _, d := range r.Data {
			a := d.Attributes
			it := libItem{
				ID:          d.ID,
				Title:       pickTitle(a.Title, d.ID),
				Description: cleanDescription(a.Description),
				Status:      a.Status,
				Lang:        a.OriginalLanguage,
			}
			if a.PublicDemographic != nil {
				it.Demographic = *a.PublicDemographic
			}
			if a.Year != nil {
				it.Year = *a.Year
			}
			for _, t := range a.Tags {
				if n := t.Attributes.Name["en"]; n != "" {
					it.Tags = append(it.Tags, n)
				}
			}
			for _, rel := range d.Relationships {
				if rel.Type == "cover_art" {
					it.cover = rel.Attributes.FileName
				}
			}
			items = append(items, it)
		}
	}
	c.addStatistics(ctx, items)
	sort.Slice(items, func(i, j int) bool {
		return strings.ToLower(items[i].Title) < strings.ToLower(items[j].Title)
	})
	return items, nil
}

// hasDownloadable reports whether a title has at least one chapter in the
// chosen language that actually has pages on MangaDex (not an external link and
// not empty), using the same content ratings the download would use.
func (c *client) hasDownloadable(ctx context.Context, id string, o options) (bool, error) {
	q := url.Values{}
	q.Set("limit", "1")
	q.Add("translatedLanguage[]", o.lang)
	q.Set("includeExternalUrl", "0")
	q.Set("includeEmptyPages", "0")
	for _, r := range o.ratings {
		q.Add("contentRating[]", r)
	}
	var r struct {
		Total int `json:"total"`
	}
	if err := c.getJSON(ctx, c.api, fmt.Sprintf("%s/manga/%s/feed?%s", apiBase, id, q.Encode()), &r); err != nil {
		return false, err
	}
	return r.Total > 0, nil
}

// loadLibraryItems returns the downloadable titles among ids, using the on-disk
// cache for anything fetched or checked recently. Only stale entries hit the API.
func (c *client) loadLibraryItems(ctx context.Context, ids []string, o options, mc *metaCache, refresh bool) ([]libItem, error) {
	now := time.Now()

	var stale []string
	for _, id := range ids {
		e := mc.Entries[id]
		if refresh || e == nil || e.Lang != o.lang || now.Sub(e.Fetched) > metaTTL {
			stale = append(stale, id)
		}
	}
	log.Printf("%d title(s) in library: %d cached, %d to fetch", len(ids), len(ids)-len(stale), len(stale))

	if len(stale) > 0 {
		fetched, err := c.mangaDetails(ctx, stale, o.lang)
		if err != nil {
			return nil, fmt.Errorf("loading titles: %w", err)
		}
		got := map[string]bool{}
		for _, it := range fetched {
			got[it.ID] = true
			mc.Entries[it.ID] = &cacheEntry{Item: it, Cover: it.cover, Lang: o.lang, HasLang: true, Fetched: now}
		}
		// Titles MangaDex doesn't return lack the language; remember that too.
		for _, id := range stale {
			if !got[id] {
				mc.Entries[id] = &cacheEntry{Lang: o.lang, Fetched: now}
			}
		}
	}

	key := o.lang + "|" + strings.Join(o.ratings, ",")
	var items []libItem
	checked := 0
	for i, id := range ids {
		e := mc.Entries[id]
		if e == nil || !e.HasLang {
			continue
		}
		if refresh || e.CheckKey != key || now.Sub(e.Checked) > checkTTL {
			ok, err := c.hasDownloadable(ctx, id, o)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", e.Item.Title, err)
			}
			e.CheckKey, e.Checked, e.Downloadable = key, time.Now(), ok
			if checked++; checked%25 == 0 {
				log.Printf("  checked %d (at %d/%d)", checked, i+1, len(ids))
			}
		}
		if !e.Downloadable {
			log.Printf("Hiding %q: no downloadable %q chapters", e.Item.Title, o.lang)
			continue
		}
		it := e.Item
		it.cover = e.Cover
		items = append(items, it)
	}
	if err := mc.save(); err != nil {
		log.Printf("warning: could not save cache: %v", err)
	}
	sort.Slice(items, func(i, j int) bool {
		return strings.ToLower(items[i].Title) < strings.ToLower(items[j].Title)
	})
	return items, nil
}

// addStatistics fills in rating and follows. Best effort: failures are ignored.
func (c *client) addStatistics(ctx context.Context, items []libItem) {
	for start := 0; start < len(items); start += 100 {
		batch := items[start:min(start+100, len(items))]
		q := url.Values{}
		for _, it := range batch {
			q.Add("manga[]", it.ID)
		}
		var r struct {
			Statistics map[string]struct {
				Rating struct {
					Average *float64 `json:"average"`
				} `json:"rating"`
				Follows int `json:"follows"`
			} `json:"statistics"`
		}
		if err := c.getJSON(ctx, c.api, apiBase+"/statistics/manga?"+q.Encode(), &r); err != nil {
			log.Printf("statistics unavailable: %v", err)
			return
		}
		for i := range batch {
			st := r.Statistics[batch[i].ID]
			items[start+i].Follows = st.Follows
			if st.Rating.Average != nil {
				items[start+i].Rating = *st.Rating.Average
			}
		}
	}
}

type uiServer struct {
	c      *client
	ctx    context.Context
	items  []libItem
	byID   map[string]libItem
	worker string // base URL of the downloader daemon

	coverDir string
	coverSem chan struct{}
}

func runLibraryUI(ctx context.Context, c *client, status, addr, cacheDir, daemonAddr string, refresh bool, opts options) error {
	log.Printf("Logging in and loading library (%s)...", status)
	ids, err := c.libraryStatuses(ctx, status)
	if err != nil {
		return fmt.Errorf("loading library: %w", err)
	}
	items, err := c.loadLibraryItems(ctx, ids, opts, loadMetaCache(cacheDir), refresh)
	if err != nil {
		return err
	}
	log.Printf("Loaded %d title(s)", len(items))

	s := &uiServer{
		c:        c,
		ctx:      ctx,
		items:    items,
		byID:     map[string]libItem{},
		worker:   "http://" + daemonAddr,
		coverDir: filepath.Join(cacheDir, "covers"),
		coverSem: make(chan struct{}, 4),
	}
	for _, it := range items {
		s.byID[it.ID] = it
	}
	go s.prefetchCovers()

	if _, err := s.workerDo(ctx, http.MethodGet, "/queue", nil); err != nil {
		log.Printf("Warning: downloader not reachable at %s. Start it with: ./mangadex-scraper -daemon", s.worker)
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(indexHTML)
	})
	mux.HandleFunc("GET /api/library", s.handleLibrary)
	mux.HandleFunc("GET /cover/{id}", s.handleCover)
	mux.HandleFunc("POST /api/download", s.handleDownload)
	mux.HandleFunc("GET /api/queue", s.handleQueue)
	mux.HandleFunc("POST /api/cancel/{id}", s.handleCancel)
	mux.HandleFunc("POST /api/clear", s.handleClear)

	srv := &http.Server{Handler: localOnly(ln.Addr().String(), mux)}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		srv.Shutdown(shutdown)
	}()

	page := "http://" + ln.Addr().String()
	log.Printf("Open %s (Ctrl-C to quit)", page)
	openBrowser(page)
	if err := srv.Serve(ln); err != http.ErrServerClosed {
		return err
	}
	return nil
}

// localOnly rejects requests whose Host header isn't our own address, which
// blocks DNS-rebinding attacks against the local server.
func localOnly(listen string, next http.Handler) http.Handler {
	_, port, _ := net.SplitHostPort(listen)
	ok := map[string]bool{
		"127.0.0.1:" + port: true,
		"localhost:" + port: true,
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !ok[r.Host] {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func openBrowser(u string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", u)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	default:
		cmd = exec.Command("xdg-open", u)
	}
	_ = cmd.Start()
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func (s *uiServer) handleLibrary(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.items)
}

// workerDo calls the downloader daemon's control API.
func (s *uiServer) workerDo(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	// The daemon is local and answers instantly; don't wait out the 60s client timeout.
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, s.worker+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := s.c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return b, fmt.Errorf("downloader: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(truncate(b, 200)))
	}
	return b, nil
}

// workerDown reports that the daemon can't be reached, in a form the page shows.
func (s *uiServer) workerDown(w http.ResponseWriter, err error) {
	log.Printf("downloader: %v", err)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadGateway)
	msg := "Downloader not reachable. Start it with: ./mangadex-scraper -daemon"
	if os.IsTimeout(err) {
		// The port is open but nothing answers: typically a daemon suspended with Ctrl-Z.
		msg = "Downloader is not responding. If you suspended it (Ctrl-Z), resume it with `bg` or `kill -CONT <pid>`."
	}
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func (s *uiServer) handleDownload(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		http.Error(w, "expected JSON", http.StatusUnsupportedMediaType)
		return
	}
	var req struct {
		IDs []string `json:"ids"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	var items []queueItem
	for _, id := range req.IDs {
		it, ok := s.byID[id]
		if !ok {
			http.Error(w, "unknown id", http.StatusBadRequest)
			return
		}
		items = append(items, queueItem{ID: id, Title: it.Title})
	}
	if len(items) == 0 {
		http.Error(w, "nothing selected", http.StatusBadRequest)
		return
	}
	body, _ := json.Marshal(map[string]any{"items": items})
	resp, err := s.workerDo(r.Context(), http.MethodPost, "/queue", body)
	if err != nil {
		if resp != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		s.workerDown(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(resp)
}

func (s *uiServer) handleQueue(w http.ResponseWriter, r *http.Request) {
	resp, err := s.workerDo(r.Context(), http.MethodGet, "/queue", nil)
	if err != nil {
		s.workerDown(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(resp)
}

func (s *uiServer) handleCancel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.byID[id]; !ok {
		http.NotFound(w, r)
		return
	}
	if _, err := s.workerDo(r.Context(), http.MethodPost, "/queue/"+id+"/cancel", nil); err != nil {
		s.workerDown(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *uiServer) handleClear(w http.ResponseWriter, r *http.Request) {
	if _, err := s.workerDo(r.Context(), http.MethodPost, "/queue/clear", nil); err != nil {
		s.workerDown(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// coverBytes returns a 256px cover thumbnail, from disk if cached, else fetched
// from MangaDex and written to the disk cache.
func (s *uiServer) coverBytes(ctx context.Context, it libItem) ([]byte, error) {
	path := filepath.Join(s.coverDir, filepath.Base(it.ID+"_"+it.cover))
	if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
		return b, nil
	}

	select {
	case s.coverSem <- struct{}{}:
		defer func() { <-s.coverSem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	u := fmt.Sprintf("https://uploads.mangadex.org/covers/%s/%s.256.jpg", it.ID, it.cover)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	req.Header.Set("User-Agent", userAgent)
	resp, err := s.c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cover HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(s.coverDir, 0o755); err == nil {
		tmp := path + ".part"
		if os.WriteFile(tmp, b, 0o644) == nil {
			_ = os.Rename(tmp, path)
		}
	}
	return b, nil
}

// prefetchCovers warms the disk cache in the background so scrolling is instant.
func (s *uiServer) prefetchCovers() {
	for _, it := range s.items {
		if s.ctx.Err() != nil {
			return
		}
		if it.cover != "" {
			_, _ = s.coverBytes(s.ctx, it)
		}
	}
}

func (s *uiServer) handleCover(w http.ResponseWriter, r *http.Request) {
	it, ok := s.byID[r.PathValue("id")]
	if !ok || it.cover == "" {
		http.NotFound(w, r)
		return
	}
	b, err := s.coverBytes(r.Context(), it)
	if err != nil {
		http.Error(w, "cover fetch failed", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	// The file name changes when the cover does, so this URL can be cached hard.
	w.Header().Set("Cache-Control", "public, max-age=604800, immutable")
	w.Write(b)
}
