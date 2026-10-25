package library

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/xiiisins/md-downloader/internal/mangadex"
)

// LoadItems returns the downloadable titles among ids, using the on-disk
// cache for anything fetched or checked recently. Only stale entries hit the API.
func LoadItems(ctx context.Context, c *mangadex.Client, ids []string, lang string, ratings []string, mc *Cache, refresh bool) ([]mangadex.Manga, error) {
	now := time.Now()

	var stale []string
	for _, id := range ids {
		e := mc.Entries[id]
		if refresh || e == nil || e.Lang != lang || now.Sub(e.Fetched) > metaTTL {
			stale = append(stale, id)
		}
	}
	log.Printf("%d title(s) in library: %d cached, %d to fetch", len(ids), len(ids)-len(stale), len(stale))

	if len(stale) > 0 {
		fetched, err := c.MangaDetails(ctx, stale, lang)
		if err != nil {
			return nil, fmt.Errorf("loading titles: %w", err)
		}
		got := map[string]bool{}
		for _, it := range fetched {
			got[it.ID] = true
			mc.Entries[it.ID] = &cacheEntry{Item: it, Cover: it.Cover, Lang: lang, HasLang: true, Fetched: now}
		}
		// Titles MangaDex doesn't return lack the language; remember that too.
		for _, id := range stale {
			if !got[id] {
				mc.Entries[id] = &cacheEntry{Lang: lang, Fetched: now}
			}
		}
	}

	key := lang + "|" + strings.Join(ratings, ",")
	var items []mangadex.Manga
	checked := 0
	for i, id := range ids {
		e := mc.Entries[id]
		if e == nil || !e.HasLang {
			continue
		}
		if refresh || e.CheckKey != key || now.Sub(e.Checked) > checkTTL {
			n, err := c.DownloadableChapters(ctx, id, lang, ratings)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", e.Item.Title, err)
			}
			e.CheckKey, e.Checked, e.Downloadable, e.Chapters = key, time.Now(), n > 0, n
			if checked++; checked%25 == 0 {
				log.Printf("  checked %d (at %d/%d)", checked, i+1, len(ids))
			}
		}
		if !e.Downloadable {
			log.Printf("Hiding %q: no downloadable %q chapters", e.Item.Title, lang)
			continue
		}
		it := e.Item
		it.Cover = e.Cover
		it.Chapters = e.Chapters
		items = append(items, it)
	}
	if err := mc.save(); err != nil {
		log.Printf("warning: could not save cache: %v", err)
	}
	sort.Slice(items, func(i, j int) bool {
		return strings.ToLower(items[i].Title) < strings.ToLower(items[j].Title)
	})
	return items, nil
}
