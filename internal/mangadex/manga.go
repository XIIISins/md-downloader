package mangadex

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// Chapter is one chapter as listed by a manga's feed.
type Chapter struct {
	ID      string
	Volume  string
	Number  string
	Title   string
	Pages   int
	Ext     string // externally hosted; not downloadable
	Updated string
}

// Downloadable reports whether the chapter's pages are hosted by MangaDex.
func (ch Chapter) Downloadable() bool { return ch.Ext == "" && ch.Pages > 0 }

// Manga is a series with the metadata the library UI shows.
type Manga struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
	Status      string   `json:"status"`
	Lang        string   `json:"lang"`
	Demographic string   `json:"demographic"`
	// ContentRating is safe, suggestive, erotica or pornographic.
	ContentRating string `json:"content_rating"`
	// TagGroups maps each of this title's Tags to its MangaDex group (format,
	// genre, theme or content).
	TagGroups map[string]string `json:"tag_groups"`
	// Chapters counts the distinct downloadable chapters in the configured
	// language: one per chapter file the downloader would write.
	Chapters int `json:"chapters"`
	// Downloaded is how many of those chapters are on disk, from the daemon's
	// last scan. DLStatus is derived from it, see DownloadStatus.
	Downloaded int     `json:"downloaded"`
	DLStatus   string  `json:"dl_status"`
	Year       int     `json:"year"`
	Rating     float64 `json:"rating"`
	Follows    int     `json:"follows"`
	Cover      string  `json:"-"` // cover file name on uploads.mangadex.org
}

// Download statuses, from how much of a series is on disk.
const (
	DLNone     = "none"
	DLPartial  = "partial"
	DLUpToDate = "up_to_date" // everything is here and the series is still running
	DLComplete = "complete"   // everything is here and the series has ended
)

// DownloadStatus derives the on-disk state from Downloaded, Chapters and Status.
// Series that are completed, cancelled or on hiatus count as complete; any other
// status is treated as ongoing.
func (m Manga) DownloadStatus() string {
	switch {
	case m.Downloaded <= 0 || m.Chapters <= 0:
		return DLNone
	case m.Downloaded < m.Chapters:
		return DLPartial
	case m.Status == "ongoing":
		return DLUpToDate
	default:
		return DLComplete
	}
}

// MangaTitle returns the English title of a series, or any title if it has none.
func (c *Client) MangaTitle(ctx context.Context, id string) (string, error) {
	var r struct {
		Data struct {
			Attributes struct {
				Title map[string]string `json:"title"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if err := c.getJSON(ctx, c.APILimit, c.BaseURL+"/manga/"+id, &r); err != nil {
		return "", err
	}
	t := r.Data.Attributes.Title
	if v := t["en"]; v != "" {
		return v, nil
	}
	for _, v := range t {
		return v, nil
	}
	return id, nil
}

// Chapters lists a series' chapters in the given language, one per
// volume/chapter number.
func (c *Client) Chapters(ctx context.Context, id, lang string, ratings []string) ([]Chapter, error) {
	return c.feed(ctx, id, lang, ratings, nil)
}

// DownloadableChapters lists the chapters of a series in lang that are hosted
// on MangaDex (no external link, not empty), one per volume/chapter number,
// exactly as Chapters would list them minus the ones that can't be downloaded.
func (c *Client) DownloadableChapters(ctx context.Context, id, lang string, ratings []string) ([]Chapter, error) {
	extra := url.Values{}
	extra.Set("includeExternalUrl", "0")
	extra.Set("includeEmptyPages", "0")
	all, err := c.feed(ctx, id, lang, ratings, extra)
	if err != nil {
		return nil, err
	}
	n := 0
	for _, ch := range all {
		if ch.Downloadable() {
			all[n] = ch
			n++
		}
	}
	return all[:n], nil
}

// feed pages through a series' chapter feed, 500 entries per request, and
// merges the entries several groups uploaded for the same chapter. extra adds
// query parameters.
func (c *Client) feed(ctx context.Context, id, lang string, ratings []string, extra url.Values) ([]Chapter, error) {
	var all []Chapter
	seen := map[string]int{} // chapter key -> index in all
	for offset := 0; ; {
		q := url.Values{}
		q.Set("limit", "500")
		q.Set("offset", strconv.Itoa(offset))
		q.Add("translatedLanguage[]", lang)
		q.Set("order[volume]", "asc")
		q.Set("order[chapter]", "asc")
		for k, vs := range extra {
			q[k] = vs
		}
		for _, r := range ratings {
			q.Add("contentRating[]", r)
		}
		var r struct {
			Total int `json:"total"`
			Data  []struct {
				ID         string `json:"id"`
				Attributes struct {
					Volume      *string `json:"volume"`
					Chapter     *string `json:"chapter"`
					Title       *string `json:"title"`
					Pages       int     `json:"pages"`
					ExternalURL *string `json:"externalUrl"`
				} `json:"attributes"`
			} `json:"data"`
		}
		if err := c.getJSON(ctx, c.APILimit, fmt.Sprintf("%s/manga/%s/feed?%s", c.BaseURL, id, q.Encode()), &r); err != nil {
			return nil, err
		}
		for _, d := range r.Data {
			a := d.Attributes
			ch := Chapter{ID: d.ID, Pages: a.Pages, Number: "oneshot"}
			if a.Chapter != nil && *a.Chapter != "" {
				ch.Number = *a.Chapter
			}
			if a.Volume != nil {
				ch.Volume = *a.Volume
			}
			if a.Title != nil {
				ch.Title = *a.Title
			}
			if a.ExternalURL != nil {
				ch.Ext = *a.ExternalURL
			}
			// Several groups may upload the same chapter. Keep the first, unless it
			// can't be downloaded (external link or no pages) and a later one can.
			key := ch.Volume + "/" + ch.Number
			if i, dup := seen[key]; dup && ch.Number != "oneshot" {
				if !all[i].Downloadable() && ch.Downloadable() {
					all[i] = ch
				}
				continue
			}
			seen[key] = len(all)
			all = append(all, ch)
		}
		offset += len(r.Data)
		if len(r.Data) == 0 || offset >= r.Total {
			break
		}
	}
	return all, nil
}

// AtHome is the image server assignment for one chapter.
type AtHome struct {
	BaseURL string `json:"baseUrl"`
	Chapter struct {
		Hash      string   `json:"hash"`
		Data      []string `json:"data"`
		DataSaver []string `json:"dataSaver"`
	} `json:"chapter"`
}

// AtHomeServer asks MangaDex which image server to fetch a chapter's pages from.
func (c *Client) AtHomeServer(ctx context.Context, chapterID string) (AtHome, error) {
	var ah AtHome
	err := c.getJSON(ctx, c.AtHomeLimit, c.BaseURL+"/at-home/server/"+chapterID, &ah)
	return ah, err
}

// LibraryStatuses returns manga IDs in the user's library with the given
// reading status (reading, on_hold, plan_to_read, dropped, re_reading, completed).
func (c *Client) LibraryStatuses(ctx context.Context, status string) ([]string, error) {
	var r struct {
		Statuses map[string]string `json:"statuses"`
	}
	u := c.BaseURL + "/manga/status"
	if status != "" && status != "all" {
		u += "?status=" + url.QueryEscape(status)
	}
	if err := c.getJSONAuth(ctx, c.APILimit, u, &r); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(r.Statuses))
	for id := range r.Statuses {
		ids = append(ids, id)
	}
	return ids, nil
}

// cleanDescription drops the "---" trailer where uploaders put links and credits.
func cleanDescription(m map[string]string) string {
	d := m["en"]
	if d == "" {
		return ""
	}
	if i := strings.Index(d, "\n---"); i >= 0 {
		d = d[:i]
	}
	return strings.TrimSpace(d)
}

func pickTitle(t map[string]string, fallback string) string {
	if v := t["en"]; v != "" {
		return v
	}
	for _, v := range t {
		return v
	}
	return fallback
}

// MangaDetails fetches titles, covers and statistics, 100 IDs per request.
// Titles with no chapters in lang are not returned.
func (c *Client) MangaDetails(ctx context.Context, ids []string, lang string) ([]Manga, error) {
	var items []Manga
	for start := 0; start < len(ids); start += 100 {
		batch := ids[start:min(start+100, len(ids))]
		q := url.Values{}
		q.Set("limit", "100")
		q.Add("includes[]", "cover_art")
		q.Add("availableTranslatedLanguage[]", lang)
		for _, r := range []string{"safe", "suggestive", "erotica", "pornographic"} {
			q.Add("contentRating[]", r)
		}
		for _, id := range batch {
			q.Add("ids[]", id)
		}
		var r struct {
			Data []struct {
				ID         string `json:"id"`
				Attributes struct {
					Title                  map[string]string `json:"title"`
					Description            map[string]string `json:"description"`
					Status                 string            `json:"status"`
					OriginalLanguage       string            `json:"originalLanguage"`
					PublicationDemographic *string           `json:"publicationDemographic"`
					ContentRating          string            `json:"contentRating"`
					Year                   *int              `json:"year"`
					Tags                   []struct {
						Attributes struct {
							Name  map[string]string `json:"name"`
							Group string            `json:"group"`
						} `json:"attributes"`
					} `json:"tags"`
				} `json:"attributes"`
				Relationships []struct {
					Type       string `json:"type"`
					Attributes struct {
						FileName string `json:"fileName"`
					} `json:"attributes"`
				} `json:"relationships"`
			} `json:"data"`
		}
		if err := c.getJSON(ctx, c.APILimit, c.BaseURL+"/manga?"+q.Encode(), &r); err != nil {
			return nil, err
		}
		for _, d := range r.Data {
			a := d.Attributes
			it := Manga{
				ID:          d.ID,
				Title:       pickTitle(a.Title, d.ID),
				Description: cleanDescription(a.Description),
				Status:      a.Status,
				Lang:        a.OriginalLanguage,

				ContentRating: a.ContentRating,
				TagGroups:     map[string]string{},
			}
			if a.PublicationDemographic != nil {
				it.Demographic = *a.PublicationDemographic
			}
			if a.Year != nil {
				it.Year = *a.Year
			}
			for _, t := range a.Tags {
				if n := t.Attributes.Name["en"]; n != "" {
					it.Tags = append(it.Tags, n)
					it.TagGroups[n] = strings.ToLower(t.Attributes.Group)
				}
			}
			for _, rel := range d.Relationships {
				if rel.Type == "cover_art" {
					it.Cover = rel.Attributes.FileName
				}
			}
			items = append(items, it)
		}
	}
	c.addStatistics(ctx, items)
	sort.Slice(items, func(i, j int) bool {
		return strings.ToLower(items[i].Title) < strings.ToLower(items[j].Title)
	})
	return items, nil
}

// addStatistics fills in rating and follows. Best effort: failures are ignored.
func (c *Client) addStatistics(ctx context.Context, items []Manga) {
	for start := 0; start < len(items); start += 100 {
		batch := items[start:min(start+100, len(items))]
		q := url.Values{}
		for _, it := range batch {
			q.Add("manga[]", it.ID)
		}
		var r struct {
			Statistics map[string]struct {
				Rating struct {
					Average *float64 `json:"average"`
				} `json:"rating"`
				Follows int `json:"follows"`
			} `json:"statistics"`
		}
		if err := c.getJSON(ctx, c.APILimit, c.BaseURL+"/statistics/manga?"+q.Encode(), &r); err != nil {
			log.Printf("statistics unavailable: %v", err)
			return
		}
		for i := range batch {
			st := r.Statistics[batch[i].ID]
			items[start+i].Follows = st.Follows
			if st.Rating.Average != nil {
				items[start+i].Rating = *st.Rating.Average
			}
		}
	}
}
