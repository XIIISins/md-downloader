package download

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// Options select what SyncManga downloads and where it goes.
type Options struct {
	Lang    string
	Out     string
	Format  string // "cbz" or "dir"
	Ratings []string
	From    float64 // first chapter number; negative means no limit
	To      float64 // last chapter number; negative means no limit
}

// Tally counts what happened to a series' chapters. Have is chapters already on
// disk; External is chapters MangaDex doesn't host (official links or no pages).
type Tally struct{ OK, Have, External, Failed int }

func (t Tally) String() string {
	s := fmt.Sprintf("%d downloaded, %d already had, %d external/unavailable", t.OK, t.Have, t.External)
	if t.Failed > 0 {
		s += fmt.Sprintf(", %d failed", t.Failed)
	}
	return s
}

// SyncManga downloads the selected chapters of one manga.
func (d *Downloader) SyncManga(ctx context.Context, id string, o Options, logf func(string, ...any)) (Tally, error) {
	var t Tally
	if err := EnsureOutDir(o.Out); err != nil {
		return t, err
	}
	title, err := d.API.MangaTitle(ctx, id)
	if err != nil {
		return t, fmt.Errorf("fetching manga: %w", err)
	}
	logf("Manga: %s", title)

	chs, err := d.API.Chapters(ctx, id, o.Lang, o.Ratings)
	if err != nil {
		return t, fmt.Errorf("fetching chapters: %w", err)
	}
	logf("Found %d chapter(s) in %q", len(chs), o.Lang)

	mangaDir := filepath.Join(o.Out, sanitize(title))
	for i, ch := range chs {
		if ctx.Err() != nil {
			break
		}
		if err := EnsureOutDir(o.Out); err != nil {
			logf("stopping: %v", err)
			return t, err
		}
		if n, err := strconv.ParseFloat(ch.Number, 64); err == nil {
			if (o.From >= 0 && n < o.From) || (o.To >= 0 && n > o.To) {
				continue
			}
		}
		if !ch.Downloadable() {
			logf("[%d/%d] Ch. %s: externally hosted or empty, skipping", i+1, len(chs), ch.Number)
			t.External++
			continue
		}

		name := "ch" + padNumber(ch.Number)
		dir := filepath.Join(mangaDir, name)
		cbz := dir + ".cbz"

		if o.Format == "cbz" {
			if _, err := os.Stat(cbz); err == nil {
				t.Have++
				continue
			}
		}

		logf("[%d/%d] %s (%d pages)", i+1, len(chs), name, ch.Pages)
		n, err := d.downloadChapter(ctx, ch, dir)
		if err != nil {
			logf("  failed (%d ok): %v", n, err)
			t.Failed++
			continue
		}
		if o.Format == "cbz" {
			if err := zipDir(dir, cbz); err != nil {
				logf("  zip failed: %v", err)
				t.Failed++
				continue
			}
			os.RemoveAll(dir)
		}
		t.OK++
	}
	logf("Done: %s -> %s", t, mangaDir)
	return t, nil
}
