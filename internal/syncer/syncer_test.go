package syncer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xiiisins/md-downloader/internal/mangadex"
	"github.com/xiiisins/md-downloader/internal/store"
	"github.com/xiiisins/md-downloader/internal/store/pg"
)

// fakeMangaDex serves a two-title library. Title "a" has chapters 1 (uploaded by
// two groups) and 2.5; title "b" has no downloadable chapters.
func fakeMangaDex(t *testing.T) *mangadex.Client {
	t.Helper()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/manga/status":
			w.Write([]byte(`{"statuses":{"a":"reading","b":"reading"}}`))
		case r.URL.Path == "/manga":
			w.Write([]byte(`{"data":[
				{"id":"a","attributes":{"title":{"en":"Alpha"},"status":"ongoing","originalLanguage":"ja","tags":[]},"relationships":[]},
				{"id":"b","attributes":{"title":{"en":"Beta"},"status":"completed","originalLanguage":"ja","tags":[]},"relationships":[]}]}`))
		case r.URL.Path == "/manga/a/feed":
			w.Write([]byte(`{"total":3,"data":[
				{"id":"x","attributes":{"volume":null,"chapter":"1","pages":10}},
				{"id":"y","attributes":{"volume":null,"chapter":"1","pages":11}},
				{"id":"z","attributes":{"volume":null,"chapter":"2.5","pages":9}}]}`))
		case strings.HasSuffix(r.URL.Path, "/feed"):
			w.Write([]byte(`{"total":0,"data":[]}`))
		default:
			w.Write([]byte(`{"statistics":{}}`))
		}
	}))
	t.Cleanup(api.Close)

	// The login endpoint is a fixed URL, so answer it from the transport.
	hc := &http.Client{Timeout: 5 * time.Second, Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPost {
			rec := httptest.NewRecorder()
			rec.WriteString(`{"access_token":"t","refresh_token":"r","expires_in":900}`)
			return rec.Result(), nil
		}
		return http.DefaultTransport.RoundTrip(r)
	})}
	c := mangadex.NewClient(hc)
	c.BaseURL = api.URL
	c.APILimit = mangadex.NewLimiter(time.Millisecond)
	c.Auth = mangadex.NewSession(hc, mangadex.Credentials{ClientID: "i", ClientSecret: "s", Username: "u", Password: "p"})
	return c
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// memStore is an in-memory Library, Progress and cache.
type memStore struct {
	mu    sync.Mutex
	items []mangadex.Manga
	exp   []store.Expected
	blob  []byte
}

func (m *memStore) ReplaceLibrary(_ context.Context, items []mangadex.Manga) error {
	m.items = items
	return nil
}
func (m *memStore) Library(context.Context) ([]mangadex.Manga, time.Time, error) {
	return m.items, time.Now(), nil
}
func (m *memStore) SetExpected(_ context.Context, e []store.Expected) error { m.exp = e; return nil }
func (m *memStore) Expected(context.Context) ([]store.Expected, error)      { return m.exp, nil }
func (m *memStore) SetDownloaded(context.Context, string, int) error        { return nil }
func (m *memStore) Downloaded(context.Context) (map[string]int, error)      { return nil, nil }
func (m *memStore) Read() ([]byte, error)                                   { return m.blob, nil }
func (m *memStore) Write(b []byte) error                                    { m.blob = b; return nil }

type syncStore interface {
	store.Library
	store.Progress
}

func TestSyncWritesExpectedChapters(t *testing.T) {
	stores := map[string]func(t *testing.T) (syncStore, Config){
		"memory": func(t *testing.T) (syncStore, Config) {
			m := &memStore{}
			return m, Config{Library: m, Progress: m, Cache: m}
		},
	}
	if url := os.Getenv("TEST_DATABASE_URL"); url != "" {
		stores["postgres"] = func(t *testing.T) (syncStore, Config) {
			ps, err := pg.Open(context.Background(), url, "syncer-test")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(ps.Close)
			ps.SetExpected(context.Background(), nil) // start empty
			ps.Blob("library_cache").Write([]byte("{}"))
			return ps, Config{Library: ps, Progress: ps, Cache: ps.Blob("library_cache")}
		}
	}

	for name, open := range stores {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			st, cfg := open(t)
			cfg.Client = fakeMangaDex(t)
			cfg.Status, cfg.Lang, cfg.Ratings, cfg.Once = "reading", "en", []string{"safe"}, true

			if err := Run(ctx, cfg); err != nil {
				t.Fatal(err)
			}
			items, _, _ := st.Library(ctx)
			if len(items) != 1 || items[0].ID != "a" {
				t.Fatalf("library = %+v, want only the downloadable title", items)
			}
			got, err := st.Expected(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || got[0].ID != "a" || got[0].Title != "Alpha" || !reflect.DeepEqual(got[0].Keys, []string{"0001", "0002.5"}) {
				t.Fatalf("expected rows = %+v, want one row for Alpha with keys 0001 and 0002.5", got)
			}
			if items[0].Chapters != 2 {
				t.Errorf("chapters = %d, want 2", items[0].Chapters)
			}

			// A second sync, served from the cache, must keep the keys.
			if err := Run(ctx, cfg); err != nil {
				t.Fatal(err)
			}
			if got, _ := st.Expected(ctx); len(got) != 1 || len(got[0].Keys) != 2 {
				t.Fatalf("after a cached resync: %+v", got)
			}
		})
	}
}
