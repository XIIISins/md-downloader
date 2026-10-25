// Package library builds the list of downloadable titles from a MangaDex
// library, caching metadata on disk so startup is fast.
package library

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/xiiisins/md-downloader/internal/mangadex"
)

const (
	metaTTL  = 24 * time.Hour // titles, tags, summaries, stats
	checkTTL = 6 * time.Hour  // "has downloadable chapters" results
)

// cacheEntry remembers everything we looked up for one library title.
type cacheEntry struct {
	Item    mangadex.Manga `json:"item"`
	Cover   string         `json:"cover"` // Manga.Cover is not serialized, so keep it here
	Lang    string         `json:"lang"`  // language the details were requested for
	HasLang bool           `json:"hasLang"`
	Fetched time.Time      `json:"fetched"`

	CheckKey     string    `json:"checkKey"` // language + ratings the check ran with
	Checked      time.Time `json:"checked"`
	Downloadable bool      `json:"downloadable"`
}

// Persister stores the cache's serialized form.
type Persister interface {
	// Read returns the stored bytes, or nil if nothing was stored yet.
	Read() ([]byte, error)
	Write([]byte) error
}

// FilePersister keeps the cache in a file.
type FilePersister string

// Read implements Persister; a missing file reads as empty.
func (f FilePersister) Read() ([]byte, error) {
	b, err := os.ReadFile(string(f))
	if os.IsNotExist(err) {
		return nil, nil
	}
	return b, err
}

// Write implements Persister, replacing the file atomically.
func (f FilePersister) Write(b []byte) error {
	if err := os.MkdirAll(filepath.Dir(string(f)), 0o755); err != nil {
		return err
	}
	tmp := string(f) + ".part"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, string(f))
}

// Cache is the metadata cache.
type Cache struct {
	p       Persister
	Entries map[string]*cacheEntry `json:"entries"`
}

// LoadCache reads the cache from p; missing or corrupt data is an empty cache.
func LoadCache(p Persister) *Cache {
	mc := &Cache{p: p, Entries: map[string]*cacheEntry{}}
	if b, err := p.Read(); err == nil && b != nil {
		_ = json.Unmarshal(b, mc)
		if mc.Entries == nil {
			mc.Entries = map[string]*cacheEntry{}
		}
	}
	return mc
}

// LoadCacheDir loads the cache file (library.json) in dir.
func LoadCacheDir(dir string) *Cache {
	return LoadCache(FilePersister(filepath.Join(dir, "library.json")))
}

func (mc *Cache) save() error {
	b, err := json.Marshal(mc)
	if err != nil {
		return err
	}
	return mc.p.Write(b)
}

// DefaultCacheDir is the per-user cache location, e.g. ~/Library/Caches/mangadex-scraper.
func DefaultCacheDir() string {
	if d, err := os.UserCacheDir(); err == nil {
		return filepath.Join(d, "mangadex-scraper")
	}
	return ".cache"
}

// DefaultStateDir holds data that must survive cache cleanups, like the download queue.
func DefaultStateDir() string {
	if d, err := os.UserConfigDir(); err == nil {
		return filepath.Join(d, "mangadex-scraper")
	}
	return ".state"
}
