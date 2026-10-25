package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// TestDownloadChapterRetriesOnFreshServer simulates an image node that fails one
// page for good, and checks the chapter completes after re-requesting a server.
func TestDownloadChapterRetriesOnFreshServer(t *testing.T) {
	backoffUnit = time.Millisecond
	defer func() { backoffUnit = time.Second }()

	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("image-bytes"))
	}))
	defer good.Close()
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/data/h/2.jpg" {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		w.Write([]byte("image-bytes"))
	}))
	defer bad.Close()

	var atHomeCalls atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		base := good.URL
		if atHomeCalls.Add(1) == 1 {
			base = bad.URL // first server is flaky, later ones are healthy
		}
		fmt.Fprintf(w, `{"baseUrl":%q,"chapter":{"hash":"h","data":["1.jpg","2.jpg","3.jpg"],"dataSaver":[]}}`, base)
	}))
	defer api.Close()

	old := apiBase
	apiBase = api.URL
	defer func() { apiBase = old }()

	c := &client{
		http:    &http.Client{Timeout: 5 * time.Second},
		api:     &limiter{interval: time.Millisecond},
		atHome:  &limiter{interval: time.Millisecond},
		workers: 2,
	}
	c.reportOff.Store(time.Now().Add(time.Hour).UnixNano()) // never report fake URLs to MangaDex

	dir := t.TempDir()
	n, err := c.downloadChapter(context.Background(), chapter{ID: "c1"}, dir)
	if err != nil || n != 3 {
		t.Fatalf("downloadChapter = %d, %v; want 3, nil", n, err)
	}
	if got := atHomeCalls.Load(); got < 2 {
		t.Errorf("at-home called %d times; want a fresh server request after the failure", got)
	}
	for _, name := range []string{"001.jpg", "002.jpg", "003.jpg"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("missing %s: %v", name, err)
		}
	}
}

func TestDownloadChapterGivesUpAfterAllRounds(t *testing.T) {
	backoffUnit = time.Millisecond
	defer func() { backoffUnit = time.Second }()

	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	defer dead.Close()
	var calls atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		fmt.Fprintf(w, `{"baseUrl":%q,"chapter":{"hash":"h","data":["1.jpg"],"dataSaver":[]}}`, dead.URL)
	}))
	defer api.Close()
	old := apiBase
	apiBase = api.URL
	defer func() { apiBase = old }()

	c := &client{
		http:    &http.Client{Timeout: 5 * time.Second},
		api:     &limiter{interval: time.Millisecond},
		atHome:  &limiter{interval: time.Millisecond},
		workers: 1,
	}
	c.reportOff.Store(time.Now().Add(time.Hour).UnixNano())

	n, err := c.downloadChapter(context.Background(), chapter{ID: "c1"}, t.TempDir())
	if err == nil || n != 0 {
		t.Fatalf("downloadChapter = %d, %v; want 0 and an error", n, err)
	}
	if got := calls.Load(); got != pageRounds {
		t.Errorf("at-home called %d times; want %d (one per round)", got, pageRounds)
	}
}

func TestTallyString(t *testing.T) {
	got := tally{ok: 4, have: 76, external: 1}.String()
	if want := "4 downloaded, 76 already had, 1 external/unavailable"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	got = tally{ok: 1, failed: 2}.String()
	if want := "1 downloaded, 0 already had, 0 external/unavailable, 2 failed"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
