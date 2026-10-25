package mangadex

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMangaDetailsParsesMetadata(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/manga"):
			w.Write([]byte(`{"data":[{"id":"a","attributes":{
				"title":{"en":"Alpha"},"description":{"en":"Text\n---\nlinks"},
				"status":"ongoing","originalLanguage":"ja",
				"publicationDemographic":"seinen","year":2022,
				"tags":[{"attributes":{"name":{"en":"Fantasy"}}}]},
				"relationships":[{"type":"cover_art","attributes":{"fileName":"c.jpg"}}]}]}`))
		case strings.HasPrefix(r.URL.Path, "/statistics/manga"):
			w.Write([]byte(`{"statistics":{"a":{"rating":{"average":8.25},"follows":1234}}}`))
		}
	}))
	defer api.Close()

	c := NewClient(&http.Client{Timeout: 5 * time.Second})
	c.BaseURL = api.URL
	c.APILimit = NewLimiter(time.Millisecond)

	items, err := c.MangaDetails(context.Background(), []string{"a"}, "en")
	if err != nil || len(items) != 1 {
		t.Fatalf("MangaDetails = %v, %v", items, err)
	}
	m := items[0]
	if m.Title != "Alpha" || m.Description != "Text" || m.Demographic != "seinen" || m.Year != 2022 ||
		m.Cover != "c.jpg" || m.Rating != 8.25 || m.Follows != 1234 || len(m.Tags) != 1 {
		t.Errorf("unexpected metadata: %+v", m)
	}
}
