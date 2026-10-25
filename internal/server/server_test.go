package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xiiisins/md-downloader/internal/mangadex"
	"github.com/xiiisins/md-downloader/internal/store"
	"github.com/xiiisins/md-downloader/web"
)

func TestEmbeddedUIServes(t *testing.T) {
	files := web.FS()
	mux := http.NewServeMux()
	mux.Handle("GET /{$}", indexHandler(files))
	mux.Handle("GET /static/", http.FileServerFS(files))

	for path, want := range map[string]string{
		"/":               "/static/app.js",
		"/static/app.js":  "fetch(",
		"/static/app.css": "--",
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), want) {
			t.Errorf("GET %s = %d, body missing %q", path, rec.Code, want)
		}
	}
}

type fakeBackend struct {
	enqueued []store.Item
	err      error
}

func (f *fakeBackend) Enqueue(_ context.Context, items []store.Item) (int, []string, error) {
	f.enqueued = append(f.enqueued, items...)
	return len(items), nil, f.err
}
func (f *fakeBackend) Cancel(context.Context, string) (bool, error) { return true, f.err }
func (f *fakeBackend) ClearFinished(context.Context) error          { return f.err }
func (f *fakeBackend) Snapshot(context.Context) ([]store.Job, []string, error) {
	return []store.Job{{ID: "a", State: store.StateQueued}}, nil, f.err
}

func newTestServer(b Backend) http.Handler {
	items := []mangadex.Manga{{ID: "a", Title: "Alpha"}}
	return New(Config{
		Client:   mangadex.NewClient(http.DefaultClient),
		Items:    func(context.Context) ([]mangadex.Manga, error) { return items, nil },
		Backend:  b,
		CoverDir: filepath.Join(os.TempDir(), "md-test-covers"),
	}).Handler()
}

func do(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestAPIRoutes(t *testing.T) {
	fb := &fakeBackend{}
	h := newTestServer(fb)

	if rec := do(h, "GET", "/api/library", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"Alpha"`) {
		t.Errorf("library: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "GET", "/healthz", ""); rec.Code != 200 {
		t.Errorf("healthz: %d", rec.Code)
	}
	if rec := do(h, "POST", "/api/download", `{"ids":["a"]}`); rec.Code != 200 || len(fb.enqueued) != 1 || fb.enqueued[0].Title != "Alpha" {
		t.Errorf("download: %d %s %+v", rec.Code, rec.Body, fb.enqueued)
	}
	if rec := do(h, "POST", "/api/download", `{"ids":["nope"]}`); rec.Code != 400 {
		t.Errorf("unknown id: %d, want 400", rec.Code)
	}
	if rec := do(h, "POST", "/api/cancel/nope", ""); rec.Code != 404 {
		t.Errorf("cancel unknown: %d, want 404", rec.Code)
	}
	if rec := do(h, "POST", "/api/cancel/a", ""); rec.Code != 204 {
		t.Errorf("cancel: %d, want 204", rec.Code)
	}
	if rec := do(h, "GET", "/api/queue", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"jobs"`) {
		t.Errorf("queue: %d %s", rec.Code, rec.Body)
	}
}

func TestBackendErrorsReachThePage(t *testing.T) {
	h := newTestServer(&fakeBackend{err: &UserError{Msg: "Downloader not reachable"}})
	rec := do(h, "GET", "/api/queue", "")
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "Downloader not reachable") {
		t.Errorf("got %d %s, want 502 with the message", rec.Code, rec.Body)
	}
	rec = do(newTestServer(&fakeBackend{err: errors.New("pq: secret detail")}), "GET", "/api/queue", "")
	if strings.Contains(rec.Body.String(), "secret detail") {
		t.Errorf("internal error text leaked to the page: %s", rec.Body)
	}
}
