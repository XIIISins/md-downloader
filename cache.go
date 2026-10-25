package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

const (
	metaTTL  = 24 * time.Hour // titles, tags, summaries, stats
	checkTTL = 6 * time.Hour  // "has downloadable chapters" results
)

// cacheEntry remembers everything we looked up for one library title.
type cacheEntry struct {
	Item    libItem   `json:"item"`
	Cover   string    `json:"cover"` // libItem.cover is unexported, so keep it here
	Lang    string    `json:"lang"`  // language the details were requested for
	HasLang bool      `json:"hasLang"`
	Fetched time.Time `json:"fetched"`

	CheckKey     string    `json:"checkKey"` // language + ratings the check ran with
	Checked      time.Time `json:"checked"`
	Downloadable bool      `json:"downloadable"`
}

type metaCache struct {
	path    string
	Entries map[string]*cacheEntry `json:"entries"`
}

// loadMetaCache reads the cache file; a missing or corrupt file is an empty cache.
func loadMetaCache(dir string) *metaCache {
	mc := &metaCache{
		path:    filepath.Join(dir, "library.json"),
		Entries: map[string]*cacheEntry{},
	}
	if b, err := os.ReadFile(mc.path); err == nil {
		_ = json.Unmarshal(b, mc)
		if mc.Entries == nil {
			mc.Entries = map[string]*cacheEntry{}
		}
	}
	return mc
}

func (mc *metaCache) save() error {
	if err := os.MkdirAll(filepath.Dir(mc.path), 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(mc)
	if err != nil {
		return err
	}
	tmp := mc.path + ".part"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, mc.path)
}

// defaultCacheDir is the per-user cache location, e.g. ~/Library/Caches/mangadex-scraper.
func defaultCacheDir() string {
	if d, err := os.UserCacheDir(); err == nil {
		return filepath.Join(d, "mangadex-scraper")
	}
	return ".cache"
}

// defaultStateDir holds data that must survive cache cleanups, like the download queue.
func defaultStateDir() string {
	if d, err := os.UserConfigDir(); err == nil {
		return filepath.Join(d, "mangadex-scraper")
	}
	return ".state"
}
