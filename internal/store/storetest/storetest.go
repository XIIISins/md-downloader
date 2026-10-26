// Package storetest is a conformance suite for store.Store implementations.
package storetest

import (
	"context"
	"errors"
	"testing"

	"github.com/xiiisins/md-downloader/internal/store"
)

// Run exercises a Store. newStore must return a fresh, empty store each call.
func Run(t *testing.T, newStore func(t *testing.T) store.Store) {
	ctx := context.Background()
	a := store.Item{ID: "00000000-0000-0000-0000-00000000000a", Title: "A"}
	b := store.Item{ID: "00000000-0000-0000-0000-00000000000b", Title: "B"}

	t.Run("enqueue skips active duplicates", func(t *testing.T) {
		s := newStore(t)
		added, skipped, err := s.Enqueue(ctx, []store.Item{a, b})
		if err != nil || added != 2 || len(skipped) != 0 {
			t.Fatalf("Enqueue = %d, %v, %v", added, skipped, err)
		}
		added, skipped, _ = s.Enqueue(ctx, []store.Item{a})
		if added != 0 || len(skipped) != 1 || skipped[0] != a.ID {
			t.Fatalf("duplicate Enqueue = %d, %v", added, skipped)
		}
	})

	t.Run("claim is fifo and marks running", func(t *testing.T) {
		s := newStore(t)
		s.Enqueue(ctx, []store.Item{a, b})
		j, err := s.Claim(ctx)
		if err != nil || j == nil || j.ID != a.ID || j.State != store.StateRunning {
			t.Fatalf("Claim = %+v, %v", j, err)
		}
		j, _ = s.Claim(ctx)
		if j == nil || j.ID != b.ID {
			t.Fatalf("second Claim = %+v", j)
		}
		if j, _ = s.Claim(ctx); j != nil {
			t.Fatalf("third Claim = %+v, want nil", j)
		}
	})

	t.Run("finish and requeue finished", func(t *testing.T) {
		s := newStore(t)
		s.Enqueue(ctx, []store.Item{a})
		s.Claim(ctx)
		s.Finish(ctx, a.ID, store.StateDone, "ok")
		jobs, _ := s.List(ctx)
		if len(jobs) != 1 || jobs[0].State != store.StateDone || jobs[0].Detail != "ok" {
			t.Fatalf("List = %+v", jobs)
		}
		added, _, _ := s.Enqueue(ctx, []store.Item{a})
		if added != 1 {
			t.Fatalf("re-enqueue finished added %d, want 1", added)
		}
		jobs, _ = s.List(ctx)
		if len(jobs) != 1 || jobs[0].State != store.StateQueued {
			t.Fatalf("List after re-enqueue = %+v", jobs)
		}
	})

	t.Run("cancel only active", func(t *testing.T) {
		s := newStore(t)
		s.Enqueue(ctx, []store.Item{a})
		if ok, _ := s.Cancel(ctx, a.ID); !ok {
			t.Fatal("Cancel of queued job = false")
		}
		if ok, _ := s.Cancel(ctx, a.ID); ok {
			t.Fatal("Cancel of canceled job = true")
		}
		if ok, _ := s.Cancel(ctx, "nope"); ok {
			t.Fatal("Cancel of unknown job = true")
		}
	})

	t.Run("clear finished keeps active", func(t *testing.T) {
		s := newStore(t)
		s.Enqueue(ctx, []store.Item{a, b})
		s.Claim(ctx)
		s.Finish(ctx, a.ID, store.StateFailed, "boom")
		s.ClearFinished(ctx)
		jobs, _ := s.List(ctx)
		if len(jobs) != 1 || jobs[0].ID != b.ID {
			t.Fatalf("List = %+v", jobs)
		}
	})

	t.Run("update of a canceled job reports it", func(t *testing.T) {
		s := newStore(t)
		s.Enqueue(ctx, []store.Item{a})
		s.Claim(ctx)
		s.Cancel(ctx, a.ID)
		if err := s.Update(ctx, a.ID, "x"); !errors.Is(err, store.ErrNotRunning) {
			t.Fatalf("Update = %v, want ErrNotRunning", err)
		}
	})

	t.Run("update sets detail", func(t *testing.T) {
		s := newStore(t)
		s.Enqueue(ctx, []store.Item{a})
		s.Claim(ctx)
		s.Update(ctx, a.ID, "[1/2] ch0001")
		jobs, _ := s.List(ctx)
		if jobs[0].Detail != "[1/2] ch0001" {
			t.Fatalf("detail = %q", jobs[0].Detail)
		}
	})
}

// RunProgress exercises a Progress. newStore must return a fresh, empty store each call.
func RunProgress(t *testing.T, newStore func(t *testing.T) store.Progress) {
	ctx := context.Background()
	a := store.Expected{ID: "a", Title: "Alpha", Keys: []string{"0001", "0002.5"}}
	b := store.Expected{ID: "b", Title: "Beta", Keys: []string{"oneshot"}}

	t.Run("empty store", func(t *testing.T) {
		s := newStore(t)
		if got, err := s.Expected(ctx); err != nil || len(got) != 0 {
			t.Fatalf("Expected = %v, %v", got, err)
		}
		if got, err := s.Downloaded(ctx); err != nil || len(got) != 0 {
			t.Fatalf("Downloaded = %v, %v", got, err)
		}
	})

	t.Run("expected round trip", func(t *testing.T) {
		s := newStore(t)
		if err := s.SetExpected(ctx, []store.Expected{b, a}); err != nil {
			t.Fatal(err)
		}
		got, err := s.Expected(ctx)
		if err != nil || len(got) != 2 || got[0].ID != "a" || got[1].ID != "b" {
			t.Fatalf("Expected = %+v, %v; want a then b", got, err)
		}
		if got[0].Title != "Alpha" || len(got[0].Keys) != 2 || got[0].Keys[1] != "0002.5" || got[0].Downloaded != 0 {
			t.Errorf("a = %+v", got[0])
		}
	})

	t.Run("downloaded survives a new expectation and unknown ids are ignored", func(t *testing.T) {
		s := newStore(t)
		s.SetExpected(ctx, []store.Expected{a, b})
		if err := s.SetDownloaded(ctx, "a", 1); err != nil {
			t.Fatal(err)
		}
		if err := s.SetDownloaded(ctx, "nope", 5); err != nil {
			t.Fatalf("unknown id: %v", err)
		}
		a2 := a
		a2.Keys = []string{"0001", "0002.5", "0003"}
		if err := s.SetExpected(ctx, []store.Expected{a2}); err != nil { // drops b
			t.Fatal(err)
		}
		got, _ := s.Expected(ctx)
		if len(got) != 1 || got[0].ID != "a" || len(got[0].Keys) != 3 || got[0].Downloaded != 1 {
			t.Fatalf("Expected = %+v, want only a with 3 keys and 1 downloaded", got)
		}
		d, err := s.Downloaded(ctx)
		if err != nil || len(d) != 1 || d["a"] != 1 {
			t.Fatalf("Downloaded = %v, %v", d, err)
		}
	})

	t.Run("nil keys", func(t *testing.T) {
		s := newStore(t)
		if err := s.SetExpected(ctx, []store.Expected{{ID: "c", Title: "C"}}); err != nil {
			t.Fatal(err)
		}
		got, _ := s.Expected(ctx)
		if len(got) != 1 || len(got[0].Keys) != 0 {
			t.Fatalf("Expected = %+v", got)
		}
	})
}
