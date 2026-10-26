// Package store defines how the download queue is persisted. The file backend
// is the default; other backends (Postgres) implement the same interface and
// must pass storetest.Run.
package store

import (
	"context"
	"errors"
	"time"

	"github.com/xiiisins/md-downloader/internal/mangadex"
)

// ErrNotRunning is returned by Update when the job is no longer running under
// this store: it was canceled elsewhere, or its lease went to another claimer.
var ErrNotRunning = errors.New("job is not running")

// Job states. Queued and running are "active": a series in either state can't
// be queued again.
const (
	StateQueued   = "queued"
	StateRunning  = "running"
	StateDone     = "done"
	StateFailed   = "failed"
	StateCanceled = "canceled"
)

// Job is one series in the queue.
type Job struct {
	ID      string    `json:"id"`
	Title   string    `json:"title"`
	State   string    `json:"state"`
	Detail  string    `json:"detail"`
	Added   time.Time `json:"added"`
	Updated time.Time `json:"updated"`
}

// Active reports whether the job is queued or running.
func (j Job) Active() bool { return j.State == StateQueued || j.State == StateRunning }

// Name is the title, or the ID if there is none.
func (j Job) Name() string {
	if j.Title != "" {
		return j.Title
	}
	return j.ID
}

// Item is a series to enqueue.
type Item struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// Store persists the queue. Implementations must be safe for concurrent use.
type Store interface {
	// Enqueue adds series to the end of the queue. Series that are already
	// queued or running are skipped and returned. A finished job for the same
	// series is replaced, so it re-enters at the back.
	Enqueue(ctx context.Context, items []Item) (added int, skipped []string, err error)
	// Cancel marks a queued or running job canceled. It reports false if there
	// is no active job with that ID.
	Cancel(ctx context.Context, id string) (bool, error)
	// ClearFinished removes every job that is not active.
	ClearFinished(ctx context.Context) error
	// List returns all jobs in queue order.
	List(ctx context.Context) ([]Job, error)
	// Claim marks the oldest queued job running and returns it, or nil if none.
	Claim(ctx context.Context) (*Job, error)
	// Update sets a running job's detail line and renews its lease. It returns
	// ErrNotRunning if the job is no longer running under this store.
	Update(ctx context.Context, id, detail string) error
	// Finish records a job's final (or re-queued) state.
	Finish(ctx context.Context, id, state, detail string) error
}

// Library persists the library listing the sync worker produces and the API serves.
type Library interface {
	// ReplaceLibrary makes items the whole listing, dropping titles not in it.
	ReplaceLibrary(ctx context.Context, items []mangadex.Manga) error
	// Library returns the listing and when it was last replaced (zero if never).
	Library(ctx context.Context) (items []mangadex.Manga, synced time.Time, err error)
}

// Expected is the set of chapter files a series should have on disk, as the
// sync worker last saw them, with how many the daemon found there.
type Expected struct {
	ID         string
	Title      string   // the series folder is named after it (download.DirName)
	Keys       []string // download.ChapterKey of each distinct downloadable chapter
	Downloaded int      // how many of Keys were on disk at the last scan
}

// Progress persists what each library series should have on disk and how much
// of it does. The sync worker writes the expectations; the daemon, which can see
// the output directory, writes the counts.
type Progress interface {
	// SetExpected replaces the expectations for the whole library. Series not in
	// it are dropped; series already known keep their Downloaded count.
	SetExpected(ctx context.Context, items []Expected) error
	// Expected returns every series' expectation, ordered by ID.
	Expected(ctx context.Context) ([]Expected, error)
	// SetDownloaded records how many of a series' chapters are on disk. Unknown
	// series are ignored.
	SetDownloaded(ctx context.Context, id string, n int) error
	// Downloaded returns the recorded counts by series ID.
	Downloaded(ctx context.Context) (map[string]int, error)
}
