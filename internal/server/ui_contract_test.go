package server

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/xiiisins/md-downloader/web"
)

// Every URL the UI requests must be a route Handler serves, with the method
// the UI uses. This catches the UI and the API drifting apart.
func TestUIRequestsMatchRoutes(t *testing.T) {
	mux, ok := newTestServer(&fakeBackend{}).(*http.ServeMux)
	if !ok {
		t.Fatalf("Handler() is %T, want *http.ServeMux", newTestServer(&fakeBackend{}))
	}
	files := web.FS()

	js, err := fs.ReadFile(files, "static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	html, err := fs.ReadFile(files, "index.html")
	if err != nil {
		t.Fatal(err)
	}

	type call struct{ method, path string }
	var calls []call

	// Path literals in app.js: post('/api/x') is a POST; any other literal
	// (fetch, img.src) is a GET. A trailing slash means an id is appended.
	lit := regexp.MustCompile(`(post\(\s*)?'(/(?:api|static|cover)/[^']*)'`)
	for _, m := range lit.FindAllStringSubmatch(string(js), -1) {
		method := "GET"
		if m[1] != "" {
			method = "POST"
		}
		path := m[2]
		if strings.HasSuffix(path, "/") {
			path += "x"
		}
		calls = append(calls, call{method, path})
	}

	// Assets referenced from index.html.
	attr := regexp.MustCompile(`(?:href|src)="(/[^"#]*)"`)
	for _, m := range attr.FindAllStringSubmatch(string(html), -1) {
		calls = append(calls, call{"GET", m[1]})
	}

	// Guard against the extraction silently matching nothing.
	for _, want := range []call{
		{"GET", "/api/library"}, {"GET", "/api/queue"}, {"POST", "/api/download"},
		{"POST", "/api/cancel/x"}, {"POST", "/api/clear"}, {"GET", "/api/cover/x"},
		{"GET", "/static/app.js"}, {"GET", "/static/app.css"},
	} {
		found := false
		for _, c := range calls {
			found = found || c == want
		}
		if !found {
			t.Errorf("did not find %s %s in the UI files", want.method, want.path)
		}
	}

	for _, c := range calls {
		req := httptest.NewRequest(c.method, c.path, nil)
		if _, pattern := mux.Handler(req); pattern == "" {
			t.Errorf("UI calls %s %s, which Handler() does not route", c.method, c.path)
		}
	}
}
