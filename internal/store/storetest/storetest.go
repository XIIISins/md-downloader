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
