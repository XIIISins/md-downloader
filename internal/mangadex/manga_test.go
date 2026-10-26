package mangadex

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
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

func chapterJSON(id, vol, ch string, pages int, ext string) string {
	q := func(v string) string {
		if v == "" {
			return "null"
		}
		return `"` + v + `"`
	}
	return fmt.Sprintf(`{"id":%q,"attributes":{"volume":%s,"chapter":%s,"title":null,"pages":%d,"externalUrl":%s}}`,
		id, q(vol), q(ch), pages, q(ext))
}

func TestDownloadableChaptersDedupesAcrossGroups(t *testing.T) {
	var queries []url.Values
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/manga/a/feed" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		q := r.URL.Query()
		queries = append(queries, q)
		// Page 1 (3 entries) and page 2 (4 entries) of 7. Chapter 1 and 2 were uploaded by two groups each.
		if q.Get("offset") == "0" {
			fmt.Fprintf(w, `{"total":7,"data":[%s,%s,%s]}`,
				chapterJSON("1a", "1", "1", 20, ""), chapterJSON("1b", "1", "1", 22, ""), chapterJSON("2a", "1", "2", 18, ""))
			return
		}
		fmt.Fprintf(w, `{"total":7,"data":[%s,%s,%s,%s]}`,
			chapterJSON("2b", "1", "2", 18, ""),
			chapterJSON("ext", "1", "3", 0, "https://example.com/3"), // can't be downloaded
			chapterJSON("2.5", "", "2.5", 10, ""),
			chapterJSON("os", "", "", 30, "")) // one-shot
	}))
	defer api.Close()

	c := NewClient(&http.Client{Timeout: 5 * time.Second})
	c.BaseURL = api.URL
	c.APILimit = NewLimiter(time.Millisecond)

	chs, err := c.DownloadableChapters(context.Background(), "a", "en", []string{"safe"})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, ch := range chs {
		got = append(got, ch.Volume+"/"+ch.Number)
	}
	if want := []string{"1/1", "1/2", "/2.5", "/oneshot"}; !reflect.DeepEqual(got, want) {
		t.Errorf("chapters = %v, want %v (duplicates merged, external link dropped)", got, want)
	}
	if len(queries) != 2 {
		t.Fatalf("%d feed requests, want 2 pages", len(queries))
	}
	q := queries[0]
	if q.Get("limit") != "500" || q.Get("includeExternalUrl") != "0" || q.Get("includeEmptyPages") != "0" ||
		q.Get("translatedLanguage[]") != "en" || q.Get("contentRating[]") != "safe" {
		t.Errorf("feed query = %v", q)
	}
}

func TestChaptersKeepsExternalOnes(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("includeExternalUrl") != "" {
			t.Errorf("Chapters must not filter external chapters: %v", r.URL.Query())
		}
		fmt.Fprintf(w, `{"total":2,"data":[%s,%s]}`, chapterJSON("e", "", "1", 0, "https://example.com"), chapterJSON("h", "", "1", 12, ""))
	}))
	defer api.Close()
	c := NewClient(&http.Client{Timeout: 5 * time.Second})
	c.BaseURL = api.URL
	c.APILimit = NewLimiter(time.Millisecond)

	chs, err := c.Chapters(context.Background(), "a", "en", nil)
	if err != nil || len(chs) != 1 || chs[0].ID != "h" {
		t.Fatalf("Chapters = %+v, %v; want the hosted copy to replace the external one", chs, err)
	}
}

func TestDownloadStatus(t *testing.T) {
	for _, c := range []struct {
		status          string
		downloaded, all int
		want            string
	}{
		{"ongoing", 0, 10, DLNone},
		{"ongoing", 4, 10, DLPartial},
		{"ongoing", 10, 10, DLUpToDate},
		{"completed", 10, 10, DLComplete},
		{"cancelled", 10, 10, DLComplete},
		{"hiatus", 10, 10, DLComplete},
		{"completed", 9, 10, DLPartial},
		{"completed", 0, 10, DLNone},
		{"ongoing", 5, 0, DLNone}, // nothing to compare against
		{"ongoing", 0, 0, DLNone},
		{"", 3, 3, DLComplete},
	} {
		m := Manga{Status: c.status, Downloaded: c.downloaded, Chapters: c.all}
		if got := m.DownloadStatus(); got != c.want {
			t.Errorf("%s %d/%d = %q, want %q", c.status, c.downloaded, c.all, got, c.want)
		}
	}
}
