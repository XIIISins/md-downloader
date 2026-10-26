package queue

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xiiisins/md-downloader/internal/download"
	"github.com/xiiisins/md-downloader/internal/mangadex"
	"github.com/xiiisins/md-downloader/internal/store"
)

func TestAfterJobRunsWhenAJobFinishes(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/feed") {
			w.Write([]byte(`{"total":0,"data":[]}`))
			return
		}
		w.Write([]byte(`{"data":{"attributes":{"title":{"en":"Alpha"}}}}`))
	}))
	defer api.Close()
	c := mangadex.NewClient(&http.Client{Timeout: 5 * time.Second})
	c.BaseURL = api.URL
	c.APILimit = mangadex.NewLimiter(time.Millisecond)

	q := New(store.OpenFile(filepath.Join(t.TempDir(), "queue.json")))
	finished := make(chan string, 1)
	q.AfterJob = func(id string) { finished <- id }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go q.Work(ctx, &download.Downloader{API: c, Workers: 1}, download.Options{Lang: "en", Out: t.TempDir(), Format: "cbz"})

	if _, _, err := q.Enqueue(ctx, []Item{{ID: "a", Title: "Alpha"}}); err != nil {
		t.Fatal(err)
	}
	select {
	case id := <-finished:
		jobs, _, _ := q.Snapshot(ctx)
		if id != "a" || len(jobs) != 1 || jobs[0].State != store.StateDone {
			t.Errorf("AfterJob(%q) with jobs %+v; the job must be recorded before the hook runs", id, jobs)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("AfterJob was not called")
	}
}
