package library

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/xiiisins/md-downloader/internal/mangadex"
)

type memPersister struct{ b []byte }

func (m *memPersister) Read() ([]byte, error) { return m.b, nil }
func (m *memPersister) Write(b []byte) error  { m.b = b; return nil }

func fakeAPI(t *testing.T) *mangadex.Client {
	t.Helper()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/manga":
			w.Write([]byte(`{"data":[{"id":"a","attributes":{"title":{"en":"Alpha"},"status":"ongoing",
				"originalLanguage":"ja","contentRating":"safe",
				"tags":[{"attributes":{"name":{"en":"Fantasy"},"group":"genre"}}]},"relationships":[]}]}`))
		case strings.HasSuffix(r.URL.Path, "/feed"):
			w.Write([]byte(`{"total":9,"data":[]}`))
		default:
			w.Write([]byte(`{"statistics":{}}`))
		}
	}))
	t.Cleanup(api.Close)
	c := mangadex.NewClient(&http.Client{Timeout: 5 * time.Second})
	c.BaseURL = api.URL
	c.APILimit = mangadex.NewLimiter(time.Millisecond)
	return c
}

func TestLoadItemsCarriesNewMetadata(t *testing.T) {
	items, err := LoadItems(context.Background(), fakeAPI(t), []string{"a"}, "en", []string{"safe"}, LoadCache(&memPersister{}), false)
	if err != nil || len(items) != 1 {
		t.Fatalf("LoadItems = %v, %v", items, err)
	}
	if it := items[0]; it.Chapters != 9 || it.ContentRating != "safe" || it.TagGroups["Fantasy"] != "genre" {
		t.Errorf("metadata not carried through: %+v", it)
	}
}

func TestFreshCacheSurvivesAReload(t *testing.T) {
	p := &memPersister{}
	mc := LoadCache(p) // nothing stored yet
	mc.Entries["a"] = &cacheEntry{Item: mangadex.Manga{ID: "a"}}
	if err := mc.save(); err != nil {
		t.Fatal(err)
	}
	if got := LoadCache(p); len(got.Entries) != 1 {
		t.Errorf("a cache saved by this version was discarded on reload")
	}
}

func TestOldCacheIsDiscarded(t *testing.T) {
	// A cache from before cacheVersion existed has fresh entries without the new fields.
	old, _ := json.Marshal(map[string]any{"entries": map[string]any{
		"a": cacheEntry{Item: mangadex.Manga{ID: "a", Title: "Alpha"}, HasLang: true, Lang: "en",
			Fetched: time.Now(), CheckKey: "en|safe", Checked: time.Now(), Downloadable: true},
	}})
	mc := LoadCache(&memPersister{b: old})
	if len(mc.Entries) != 0 {
		t.Fatalf("stale-version cache kept %d entries, want 0", len(mc.Entries))
	}

	items, err := LoadItems(context.Background(), fakeAPI(t), []string{"a"}, "en", []string{"safe"}, mc, false)
	if err != nil || len(items) != 1 || items[0].Chapters != 9 || items[0].ContentRating != "safe" {
		t.Fatalf("after reset: %+v, %v", items, err)
	}

	// The rewritten cache is current, so a second load keeps it.
	again := LoadCache(&memPersister{b: mustSave(t, mc)})
	if len(again.Entries) != 1 {
		t.Errorf("current-version cache dropped its entries")
	}
}

func mustSave(t *testing.T, mc *Cache) []byte {
	t.Helper()
	b, err := json.Marshal(mc)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
