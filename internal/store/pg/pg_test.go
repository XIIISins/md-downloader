package pg_test

import (
	"context"
	"os"
	"testing"

	"github.com/xiiisins/md-downloader/internal/store"
	"github.com/xiiisins/md-downloader/internal/store/pg"
	"github.com/xiiisins/md-downloader/internal/store/storetest"
)

// testURL returns the database to test against, or skips. The tests create and
// empty the jobs table, so point TEST_DATABASE_URL at a throwaway database.
func testURL(t *testing.T) string {
	u := os.Getenv("TEST_DATABASE_URL")
	if u == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	return u
}

func open(t *testing.T, owner string) *pg.Store {
	s, err := pg.Open(context.Background(), testURL(t), owner)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

func TestPG(t *testing.T) {
	storetest.Run(t, func(t *testing.T) store.Store {
		s := open(t, "test")
		if err := s.ClearFinished(context.Background()); err != nil {
			t.Fatal(err)
		}
		// ClearFinished keeps active jobs, so wipe them too.
		jobs, _ := s.List(context.Background())
		for _, j := range jobs {
			s.Cancel(context.Background(), j.ID)
		}
		s.ClearFinished(context.Background())
		return s
	})
}

func TestTwoOwnersNeverShareAJob(t *testing.T) {
	ctx := context.Background()
	a, b := open(t, "a"), open(t, "b")
	a.ClearFinished(ctx)
	jobs, _ := a.List(ctx)
	for _, j := range jobs {
		a.Cancel(ctx, j.ID)
	}
	a.ClearFinished(ctx)

	a.Enqueue(ctx, []store.Item{{ID: "only", Title: "Only"}})
	ja, _ := a.Claim(ctx)
	jb, _ := b.Claim(ctx)
	if ja == nil || jb != nil {
		t.Fatalf("a=%v b=%v; the single job must go to exactly one claimer", ja, jb)
	}
}
