package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
