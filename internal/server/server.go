// Package server runs the local library web UI. It serves the page and covers
// and proxies queue operations to the downloader daemon.
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/xiiisins/md-downloader/internal/download"
	"github.com/xiiisins/md-downloader/internal/httpx"
	"github.com/xiiisins/md-downloader/internal/library"
	"github.com/xiiisins/md-downloader/internal/mangadex"
	"github.com/xiiisins/md-downloader/internal/queue"
	"github.com/xiiisins/md-downloader/web"
)

type Server struct {
	c      *mangadex.Client
	ctx    context.Context
	items  []mangadex.Manga
	byID   map[string]mangadex.Manga
	worker string // base URL of the downloader daemon

	coverDir string
	coverSem chan struct{}
}

func Run(ctx context.Context, c *mangadex.Client, status, addr, cacheDir, daemonAddr string, refresh bool, staticDir string, opts download.Options) error {
	log.Printf("Logging in and loading library (%s)...", status)
	ids, err := c.LibraryStatuses(ctx, status)
	if err != nil {
		return fmt.Errorf("loading library: %w", err)
	}
	items, err := library.LoadItems(ctx, c, ids, opts.Lang, opts.Ratings, library.LoadCacheDir(cacheDir), refresh)
	if err != nil {
		return err
	}
	log.Printf("Loaded %d title(s)", len(items))

	s := &Server{
		c:        c,
		ctx:      ctx,
		items:    items,
		byID:     map[string]mangadex.Manga{},
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
	// With -static the files are read from disk on every request, so UI edits
	// show up on reload without rebuilding.
	files := web.FS()
	if staticDir != "" {
		files = os.DirFS(staticDir)
		log.Printf("Serving UI files from %s", staticDir)
	}
	mux.Handle("GET /{$}", indexHandler(files))
	mux.Handle("GET /static/", http.FileServerFS(files))
	mux.HandleFunc("GET /api/library", s.handleLibrary)
	mux.HandleFunc("GET /cover/{id}", s.handleCover)
	mux.HandleFunc("POST /api/download", s.handleDownload)
	mux.HandleFunc("GET /api/queue", s.handleQueue)
	mux.HandleFunc("POST /api/cancel/{id}", s.handleCancel)
	mux.HandleFunc("POST /api/clear", s.handleClear)

	srv := &http.Server{Handler: httpx.LocalOnly(ln.Addr().String(), mux)}
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

func (s *Server) handleLibrary(w http.ResponseWriter, r *http.Request) {
	httpx.WriteJSON(w, s.items)
}

// workerDo calls the downloader daemon's control API.
func (s *Server) workerDo(ctx context.Context, method, path string, body []byte) ([]byte, error) {
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
	resp, err := s.c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return b, fmt.Errorf("downloader: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(httpx.Truncate(b, 200)))
	}
	return b, nil
}

// workerDown reports that the daemon can't be reached, in a form the page shows.
func (s *Server) workerDown(w http.ResponseWriter, err error) {
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

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
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
	var items []queue.Item
	for _, id := range req.IDs {
		it, ok := s.byID[id]
		if !ok {
			http.Error(w, "unknown id", http.StatusBadRequest)
			return
		}
		items = append(items, queue.Item{ID: id, Title: it.Title})
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

func (s *Server) handleQueue(w http.ResponseWriter, r *http.Request) {
	resp, err := s.workerDo(r.Context(), http.MethodGet, "/queue", nil)
	if err != nil {
		s.workerDown(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(resp)
}

func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
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

func (s *Server) handleClear(w http.ResponseWriter, r *http.Request) {
	if _, err := s.workerDo(r.Context(), http.MethodPost, "/queue/clear", nil); err != nil {
		s.workerDown(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// coverBytes returns a 256px cover thumbnail, from disk if cached, else fetched
// from MangaDex and written to the disk cache.
func (s *Server) coverBytes(ctx context.Context, it mangadex.Manga) ([]byte, error) {
	path := filepath.Join(s.coverDir, filepath.Base(it.ID+"_"+it.Cover))
	if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
		return b, nil
	}

	select {
	case s.coverSem <- struct{}{}:
		defer func() { <-s.coverSem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	u := fmt.Sprintf("https://uploads.mangadex.org/covers/%s/%s.256.jpg", it.ID, it.Cover)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	req.Header.Set("User-Agent", mangadex.UserAgent)
	resp, err := s.c.HTTP.Do(req)
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
func (s *Server) prefetchCovers() {
	for _, it := range s.items {
		if s.ctx.Err() != nil {
			return
		}
		if it.Cover != "" {
			_, _ = s.coverBytes(s.ctx, it)
		}
	}
}

func (s *Server) handleCover(w http.ResponseWriter, r *http.Request) {
	it, ok := s.byID[r.PathValue("id")]
	if !ok || it.Cover == "" {
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

// indexHandler serves index.html from files.
func indexHandler(files fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := fs.ReadFile(files, "index.html")
		if err != nil {
			http.Error(w, "UI files missing", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(b)
	})
}
