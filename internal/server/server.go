// Package server runs the library web UI's HTTP API. It serves the page, the
// library listing and covers, and forwards queue operations to a Backend: the
// local downloader daemon, or the shared database.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/xiiisins/md-downloader/internal/httpx"
	"github.com/xiiisins/md-downloader/internal/mangadex"
	"github.com/xiiisins/md-downloader/internal/store"
	"github.com/xiiisins/md-downloader/web"
)

// Backend is where queue operations go.
type Backend interface {
	Enqueue(ctx context.Context, items []store.Item) (added int, skipped []string, err error)
	Cancel(ctx context.Context, id string) (bool, error)
	ClearFinished(ctx context.Context) error
	Snapshot(ctx context.Context) (jobs []store.Job, logs []string, err error)
}

// UserError is a Backend error whose message is safe and useful to show in the UI.
type UserError struct{ Msg string }

func (e *UserError) Error() string { return e.Msg }

// Config describes a Server.
type Config struct {
	Client    *mangadex.Client
	Items     func(ctx context.Context) ([]mangadex.Manga, error) // the library listing
	Backend   Backend
	CoverDir  string
	StaticDir string // serve UI files from disk instead of the embedded copy
}

// Server is the HTTP API behind the UI.
type Server struct {
	cfg      Config
	coverSem chan struct{}

	mu     sync.Mutex
	items  []mangadex.Manga
	byID   map[string]mangadex.Manga
	loaded time.Time
}

// itemsTTL bounds how stale the listing served from Config.Items can be.
const itemsTTL = 15 * time.Second

// New returns a Server for cfg.
func New(cfg Config) *Server {
	return &Server{cfg: cfg, coverSem: make(chan struct{}, 4)}
}

// list returns the library listing, re-reading it from the source when stale.
func (s *Server) list(ctx context.Context) ([]mangadex.Manga, map[string]mangadex.Manga, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.byID == nil || time.Since(s.loaded) > itemsTTL {
		items, err := s.cfg.Items(ctx)
		if err != nil {
			if s.byID != nil {
				return s.items, s.byID, nil // keep serving the last good listing
			}
			return nil, nil, err
		}
		s.items, s.loaded = items, time.Now()
		s.byID = make(map[string]mangadex.Manga, len(items))
		for _, it := range items {
			s.byID[it.ID] = it
		}
	}
	return s.items, s.byID, nil
}

// Handler returns the routes. Cover images are available at /cover/{id} and
// /api/cover/{id}, so a reverse proxy only needs to forward /api/*.
func (s *Server) Handler() http.Handler {
	files := web.FS()
	if s.cfg.StaticDir != "" {
		// Read from disk on every request so UI edits show up on reload.
		files = os.DirFS(s.cfg.StaticDir)
		log.Printf("Serving UI files from %s", s.cfg.StaticDir)
	}
	mux := http.NewServeMux()
	mux.Handle("GET /{$}", indexHandler(files))
	mux.Handle("GET /static/", http.FileServerFS(files))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok\n") })
	mux.HandleFunc("GET /api/library", s.handleLibrary)
	mux.HandleFunc("GET /cover/{id}", s.handleCover)
	mux.HandleFunc("GET /api/cover/{id}", s.handleCover)
	mux.HandleFunc("POST /api/download", s.handleDownload)
	mux.HandleFunc("GET /api/queue", s.handleQueue)
	mux.HandleFunc("POST /api/cancel/{id}", s.handleCancel)
	mux.HandleFunc("POST /api/clear", s.handleClear)
	return mux
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

// backendDown reports that the queue can't be reached, in a form the page shows.
func (s *Server) backendDown(w http.ResponseWriter, err error) {
	log.Printf("queue backend: %v", err)
	msg := "Queue unavailable. Try again shortly."
	var ue *UserError
	if errors.As(err, &ue) {
		msg = ue.Msg
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadGateway)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func (s *Server) handleLibrary(w http.ResponseWriter, r *http.Request) {
	items, _, err := s.list(r.Context())
	if err != nil {
		log.Printf("library: %v", err)
		http.Error(w, "library unavailable", http.StatusBadGateway)
		return
	}
	if items == nil {
		items = []mangadex.Manga{}
	}
	httpx.WriteJSON(w, items)
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
	_, byID, err := s.list(r.Context())
	if err != nil {
		http.Error(w, "library unavailable", http.StatusBadGateway)
		return
	}
	var items []store.Item
	for _, id := range req.IDs {
		it, ok := byID[id]
		if !ok {
			http.Error(w, "unknown id", http.StatusBadRequest)
			return
		}
		items = append(items, store.Item{ID: id, Title: it.Title})
	}
	if len(items) == 0 {
		http.Error(w, "nothing selected", http.StatusBadRequest)
		return
	}
	added, skipped, err := s.cfg.Backend.Enqueue(r.Context(), items)
	if err != nil {
		s.backendDown(w, err)
		return
	}
	httpx.WriteJSON(w, map[string]any{"added": added, "skipped": skipped})
}

func (s *Server) handleQueue(w http.ResponseWriter, r *http.Request) {
	jobs, logs, err := s.cfg.Backend.Snapshot(r.Context())
	if err != nil {
		s.backendDown(w, err)
		return
	}
	httpx.WriteJSON(w, map[string]any{"jobs": jobs, "log": logs})
}

func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	_, byID, err := s.list(r.Context())
	if err != nil {
		http.Error(w, "library unavailable", http.StatusBadGateway)
		return
	}
	if _, ok := byID[id]; !ok {
		http.NotFound(w, r)
		return
	}
	if _, err := s.cfg.Backend.Cancel(r.Context(), id); err != nil {
		s.backendDown(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleClear(w http.ResponseWriter, r *http.Request) {
	if err := s.cfg.Backend.ClearFinished(r.Context()); err != nil {
		s.backendDown(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// coverBytes returns a 256px cover thumbnail, from disk if cached, else fetched
// from MangaDex and written to the disk cache.
func (s *Server) coverBytes(ctx context.Context, it mangadex.Manga) ([]byte, error) {
	path := filepath.Join(s.cfg.CoverDir, filepath.Base(it.ID+"_"+it.Cover))
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
	resp, err := s.cfg.Client.HTTP.Do(req)
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
	if err := os.MkdirAll(s.cfg.CoverDir, 0o755); err == nil {
		tmp := path + ".part"
		if os.WriteFile(tmp, b, 0o644) == nil {
			_ = os.Rename(tmp, path)
		}
	}
	return b, nil
}

// PrefetchCovers warms the disk cache in the background so scrolling is instant.
func (s *Server) PrefetchCovers(ctx context.Context) {
	items, _, err := s.list(ctx)
	if err != nil {
		return
	}
	for _, it := range items {
		if ctx.Err() != nil {
			return
		}
		if it.Cover != "" {
			_, _ = s.coverBytes(ctx, it)
		}
	}
}

func (s *Server) handleCover(w http.ResponseWriter, r *http.Request) {
	_, byID, err := s.list(r.Context())
	it, ok := byID[r.PathValue("id")]
	if err != nil || !ok || it.Cover == "" {
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
