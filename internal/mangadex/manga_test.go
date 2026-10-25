package mangadex

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
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
				"publicationDemographic":"seinen","year":2022,"contentRating":"suggestive",
				"tags":[{"attributes":{"name":{"en":"Fantasy"},"group":"genre"}},
					{"attributes":{"name":{"en":"Gore"},"group":"content"}},
					{"attributes":{"name":{"en":"Long Strip"},"group":"format"}}]},
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
		m.Cover != "c.jpg" || m.Rating != 8.25 || m.Follows != 1234 || len(m.Tags) != 3 {
		t.Errorf("unexpected metadata: %+v", m)
	}
	if m.ContentRating != "suggestive" {
		t.Errorf("ContentRating = %q, want suggestive", m.ContentRating)
	}
	want := map[string]string{"Fantasy": "genre", "Gore": "content", "Long Strip": "format"}
	if len(m.TagGroups) != len(want) {
		t.Errorf("TagGroups = %v, want %v", m.TagGroups, want)
	}
	for tag, group := range want {
		if m.TagGroups[tag] != group {
			t.Errorf("TagGroups[%q] = %q, want %q", tag, m.TagGroups[tag], group)
		}
	}
}

func TestDownloadableChaptersReadsFeedTotal(t *testing.T) {
	var query url.Values
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/manga/a/feed" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		query = r.URL.Query()
		w.Write([]byte(`{"total":42,"data":[]}`))
	}))
	defer api.Close()

	c := NewClient(&http.Client{Timeout: 5 * time.Second})
	c.BaseURL = api.URL
	c.APILimit = NewLimiter(time.Millisecond)

	n, err := c.DownloadableChapters(context.Background(), "a", "en", []string{"safe"})
	if err != nil || n != 42 {
		t.Fatalf("DownloadableChapters = %d, %v, want 42", n, err)
	}
	if query.Get("limit") != "1" || query.Get("includeExternalUrl") != "0" || query.Get("includeEmptyPages") != "0" {
		t.Errorf("feed query = %v, want limit=1 and external/empty chapters excluded", query)
	}
	if ok, err := c.HasDownloadable(context.Background(), "a", "en", nil); err != nil || !ok {
		t.Errorf("HasDownloadable = %v, %v, want true", ok, err)
	}
}
