package progress

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/xiiisins/md-downloader/internal/download"
	"github.com/xiiisins/md-downloader/internal/store"
)

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCount(t *testing.T) {
	out := t.TempDir()
	keys := []string{"0001", "0002", "0002.5", "0003", "oneshot"}
	dir := filepath.Join(out, "Alpha")
	for _, f := range []string{"ch0001.cbz", "ch0002.cbz", "ch0002.5.cbz"} {
		touch(t, filepath.Join(dir, f))
	}

	cases := []struct {
		name  string
		extra []string // files to add to the folder first
		want  int
	}{
		{"partial", nil, 3},
		{"stray and unexpected files do not count", []string{"ch0099.cbz", "notes.txt", "ch0003.cbz.part", "cover.jpg"}, 3},
		{"complete", []string{"ch0003.cbz", "chIDONTEXIST.zip", "choneshot.cbz"}, 5},
	}
	for _, c := range cases {
		for _, f := range c.extra {
			touch(t, filepath.Join(dir, f))
		}
		if got, err := Count(out, "cbz", "Alpha", keys); err != nil || got != c.want {
			t.Errorf("%s: Count = %d, %v, want %d", c.name, got, err, c.want)
		}
	}

	// A folder where a file is expected doesn't count in cbz format.
	if err := os.MkdirAll(filepath.Join(out, "Beta", "ch0001.cbz"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, _ := Count(out, "cbz", "Beta", []string{"0001"}); got != 0 {
		t.Errorf("directory named like a cbz counted: %d", got)
	}
	// Repeated keys count once.
	if got, _ := Count(out, "cbz", "Alpha", []string{"0001", "0001"}); got != 1 {
		t.Errorf("duplicate keys: Count = %d, want 1", got)
	}
	// Missing series folder: nothing downloaded, not an error.
	if got, err := Count(out, "cbz", "Nope", keys); err != nil || got != 0 {
		t.Errorf("missing folder: %d, %v", got, err)
	}
	// Titles are mapped to folders the way the downloader names them.
	touch(t, filepath.Join(out, download.DirName(`Who? "Me": a/b`), "ch0001.cbz"))
	if got, _ := Count(out, "cbz", `Who? "Me": a/b`, []string{"0001"}); got != 1 {
		t.Errorf("sanitized title folder not found: %d", got)
	}
}

func TestCountDirFormat(t *testing.T) {
	out := t.TempDir()
	touch(t, filepath.Join(out, "Alpha", "ch0001", "001.jpg"))
	touch(t, filepath.Join(out, "Alpha", "ch0002.cbz")) // wrong format for this setting
	if got, err := Count(out, "dir", "Alpha", []string{"0001", "0002"}); err != nil || got != 1 {
		t.Errorf("Count = %d, %v, want 1", got, err)
	}
}

func newScanner(t *testing.T, out string) (*Scanner, *store.File) {
	t.Helper()
	st := store.OpenFile(filepath.Join(t.TempDir(), "queue.json"))
	err := st.SetExpected(context.Background(), []store.Expected{
		{ID: "a", Title: "Alpha", Keys: []string{"0001", "0002"}},
		{ID: "b", Title: "Beta", Keys: []string{"0001"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return &Scanner{Store: st, Out: out, Format: "cbz"}, st
}

func TestScanAllAndOne(t *testing.T) {
	ctx := context.Background()
	out := t.TempDir()
	touch(t, filepath.Join(out, "Alpha", "ch0001.cbz"))
	touch(t, filepath.Join(out, "Beta", "ch0001.cbz"))
	sc, st := newScanner(t, out)

	if err := sc.ScanAll(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.Downloaded(ctx); got["a"] != 1 || got["b"] != 1 {
		t.Fatalf("after ScanAll: %v", got)
	}

	// A chapter lands, and another is deleted behind the app's back.
	touch(t, filepath.Join(out, "Alpha", "ch0002.cbz"))
	os.Remove(filepath.Join(out, "Beta", "ch0001.cbz"))
	if err := sc.ScanOne(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.Downloaded(ctx); got["a"] != 2 || got["b"] != 1 {
		t.Fatalf("ScanOne must touch only its title: %v", got)
	}
	if err := sc.ScanAll(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.Downloaded(ctx); got["a"] != 2 || got["b"] != 0 {
		t.Fatalf("after deletion: %v", got)
	}
	if err := sc.ScanOne(ctx, "unknown"); err != nil {
		t.Errorf("unknown title: %v", err)
	}
}

func TestUnavailableOutputKeepsLastCounts(t *testing.T) {
	ctx := context.Background()
	out := t.TempDir()
	touch(t, filepath.Join(out, "Alpha", "ch0001.cbz"))
	touch(t, filepath.Join(out, "Alpha", "ch0002.cbz"))
	sc, st := newScanner(t, out)
	if err := sc.ScanAll(ctx); err != nil {
		t.Fatal(err)
	}

	for name, dir := range map[string]string{
		"missing": filepath.Join(out, "gone"),
		"empty":   t.TempDir(), // an unmounted share leaves an empty mount point
	} {
		sc.Out = dir
		if err := sc.ScanAll(ctx); !errors.Is(err, download.ErrOutputUnavailable) {
			t.Errorf("%s: ScanAll = %v, want ErrOutputUnavailable", name, err)
		}
		if err := sc.ScanOne(ctx, "a"); !errors.Is(err, download.ErrOutputUnavailable) {
			t.Errorf("%s: ScanOne = %v, want ErrOutputUnavailable", name, err)
		}
		if got, _ := st.Downloaded(ctx); got["a"] != 2 {
			t.Fatalf("%s: counts were zeroed: %v", name, got)
		}
	}
}
