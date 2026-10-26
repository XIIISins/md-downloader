package store_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/xiiisins/md-downloader/internal/store"
	"github.com/xiiisins/md-downloader/internal/store/storetest"
)

func TestFile(t *testing.T) {
	storetest.Run(t, func(t *testing.T) store.Store {
		return store.OpenFile(filepath.Join(t.TempDir(), "queue.json"))
	})
}

func TestFileResumesRunningJobs(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "queue.json")
	f := store.OpenFile(path)
	f.Enqueue(ctx, []store.Item{{ID: "x", Title: "X"}})
	f.Claim(ctx)

	jobs, _ := store.OpenFile(path).List(ctx)
	if len(jobs) != 1 || jobs[0].State != store.StateQueued || jobs[0].Detail != "resuming after restart" {
		t.Fatalf("after restart: %+v", jobs)
	}
}

func TestFileProgress(t *testing.T) {
	storetest.RunProgress(t, func(t *testing.T) store.Progress {
		return store.OpenFile(filepath.Join(t.TempDir(), "queue.json"))
	})
}

func TestFileProgressPersists(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "queue.json")
	f := store.OpenFile(path)
	f.SetExpected(ctx, []store.Expected{{ID: "a", Title: "Alpha", Keys: []string{"0001"}}})
	f.SetDownloaded(ctx, "a", 1)

	got, _ := store.OpenFile(path).Expected(ctx)
	if len(got) != 1 || got[0].Downloaded != 1 || got[0].Keys[0] != "0001" {
		t.Fatalf("after restart: %+v", got)
	}
}
