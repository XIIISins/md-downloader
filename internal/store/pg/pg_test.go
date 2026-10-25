package pg_test

import (
	"context"
	"os"
	"testing"

	"github.com/xiiisins/md-downloader/internal/mangadex"
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

func TestLibraryRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := open(t, "lib")
	in := []mangadex.Manga{
		{ID: "b", Title: "beta", Tags: []string{"x"}, Year: 2020, Cover: "b.jpg",
			ContentRating: "erotica", TagGroups: map[string]string{"x": "theme"}, Chapters: 12},
		{ID: "a", Title: "Alpha", Rating: 8.5, Cover: "a.jpg"},
	}
	if err := s.ReplaceLibrary(ctx, in); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceLibrary(ctx, in[:1]); err != nil { // drops "a"
		t.Fatal(err)
	}
	out, synced, err := s.Library(ctx)
	if err != nil || len(out) != 1 || out[0].ID != "b" || out[0].Cover != "b.jpg" || out[0].Year != 2020 || synced.IsZero() {
		t.Fatalf("Library = %+v, %v, %v", out, synced, err)
	}
	if got := out[0]; got.ContentRating != "erotica" || got.TagGroups["x"] != "theme" || got.Chapters != 12 {
		t.Errorf("new metadata fields lost in the round trip: %+v", got)
	}
}

func TestBlob(t *testing.T) {
	s := open(t, "blob")
	b := s.Blob("test")
	if err := b.Write([]byte("one")); err != nil {
		t.Fatal(err)
	}
	if err := b.Write([]byte("two")); err != nil {
		t.Fatal(err)
	}
	got, err := b.Read()
	if err != nil || string(got) != "two" {
		t.Fatalf("Read = %q, %v", got, err)
	}
	if got, _ := s.Blob("missing").Read(); got != nil {
		t.Fatalf("missing blob read %q, want nil", got)
	}
}
