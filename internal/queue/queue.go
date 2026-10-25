// Package queue runs the download worker over a persistent store and exposes
// the daemon's local control API.
package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/xiiisins/md-downloader/internal/download"
	"github.com/xiiisins/md-downloader/internal/httpx"
	"github.com/xiiisins/md-downloader/internal/mangadex"
	"github.com/xiiisins/md-downloader/internal/store"
)

// Item is a series to enqueue.
type Item = store.Item

// Queue pairs a Store with the in-memory state of the job being worked on
// (so it can be canceled) and a ring of recent log lines.
type Queue struct {
	st   store.Store
	wake chan struct{}

	mu           sync.Mutex
	logs         []string
	curID        string
	cancelCur    context.CancelFunc
	userCanceled bool
	detail       string // latest progress line of the current job
}

const heartbeatEvery = 30 * time.Second

// New returns a Queue backed by st.
func New(st store.Store) *Queue {
	return &Queue{st: st, wake: make(chan struct{}, 1)}
}

// Enqueue adds series; ones already queued or running are returned as skipped.
func (q *Queue) Enqueue(ctx context.Context, items []Item) (added int, skipped []string, err error) {
	added, skipped, err = q.st.Enqueue(ctx, items)
	select {
	case q.wake <- struct{}{}:
	default:
	}
	return added, skipped, err
}

// Cancel stops a queued or running series.
func (q *Queue) Cancel(ctx context.Context, id string) (bool, error) {
	q.mu.Lock()
	if id == q.curID && q.cancelCur != nil {
		q.userCanceled = true
		q.cancelCur()
		q.mu.Unlock()
		return true, nil // Work records the final state
	}
	q.mu.Unlock()
	return q.st.Cancel(ctx, id)
}

// ClearFinished drops finished jobs from the list.
func (q *Queue) ClearFinished(ctx context.Context) error { return q.st.ClearFinished(ctx) }

// Snapshot returns all jobs and the recent log lines.
func (q *Queue) Snapshot(ctx context.Context) ([]store.Job, []string, error) {
	jobs, err := q.st.List(ctx)
	if err != nil {
		return nil, nil, err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	return jobs, append([]string(nil), q.logs...), nil
}

func (q *Queue) note(ctx context.Context, j *store.Job, line string) {
	q.touch(ctx, j.ID, line)
	q.mu.Lock()
	defer q.mu.Unlock()
	q.detail = line
	q.logs = append(q.logs, j.Name()+": "+line)
	if len(q.logs) > 200 {
		q.logs = q.logs[len(q.logs)-200:]
	}
}

// touch records progress, which also renews the job's lease. If the store says
// the job is no longer ours (canceled from the API, or the lease went to another
// daemon) the running download is stopped.
func (q *Queue) touch(ctx context.Context, id, detail string) {
	if err := q.st.Update(ctx, id, detail); errors.Is(err, store.ErrNotRunning) {
		q.mu.Lock()
		if id == q.curID && q.cancelCur != nil {
			q.userCanceled = true
			q.cancelCur()
		}
		q.mu.Unlock()
	}
}

// heartbeat renews the current job's lease while a long chapter downloads.
func (q *Queue) heartbeat(ctx context.Context, id string) {
	t := time.NewTicker(heartbeatEvery)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			q.mu.Lock()
			detail := q.detail
			q.mu.Unlock()
			q.touch(ctx, id, detail)
		case <-ctx.Done():
			return
		}
	}
}

// Work runs queued series one at a time until ctx is canceled.
func (q *Queue) Work(ctx context.Context, d *download.Downloader, o download.Options) {
	// Bookkeeping must still be written while shutting down, so it can't use ctx.
	bg := context.Background()
	waiting := false
	for ctx.Err() == nil {
		// Don't start (and fail) jobs while the output directory is missing,
		// e.g. a dropped NAS mount. The queue is kept and resumes by itself.
		if err := download.EnsureOutDir(o.Out); err != nil {
			if !waiting {
				log.Printf("%v; pausing the queue until it is back", err)
				waiting = true
			}
			select {
			case <-time.After(30 * time.Second):
			case <-ctx.Done():
			}
			continue
		}
		if waiting {
			log.Printf("output directory is back; resuming the queue")
			waiting = false
		}

		j, err := q.st.Claim(bg)
		if err != nil {
			log.Printf("claiming a job: %v", err)
		}
		if j == nil {
			wait := time.After(time.Hour)
			if err != nil {
				wait = time.After(10 * time.Second)
			}
			select {
			case <-q.wake:
			case <-wait:
			case <-ctx.Done():
			}
			continue
		}

		jctx, cancel := context.WithCancel(ctx)
		q.mu.Lock()
		q.curID, q.cancelCur, q.userCanceled = j.ID, cancel, false
		q.detail = j.Detail
		q.mu.Unlock()
		go q.heartbeat(jctx, j.ID)

		logf := func(format string, args ...any) {
			line := fmt.Sprintf(format, args...)
			log.Printf("%s: %s", j.Name(), line)
			q.note(bg, j, line)
		}
		t, err := d.SyncManga(jctx, j.ID, o, logf)
		cancel()

		q.mu.Lock()
		canceled := q.userCanceled
		q.curID, q.cancelCur, q.userCanceled = "", nil, false
		q.mu.Unlock()

		var state, detail string
		switch {
		case ctx.Err() != nil && !canceled:
			state, detail = store.StateQueued, "interrupted; will resume"
		case canceled:
			state, detail = store.StateCanceled, "canceled"
		case errors.Is(err, download.ErrOutputUnavailable):
			state, detail = store.StateQueued, "waiting for output directory"
		case err != nil:
			state, detail = store.StateFailed, err.Error()
		case t.Failed > 0:
			state, detail = store.StateFailed, t.String()
		default:
			state, detail = store.StateDone, t.String()
		}
		if err := q.st.Finish(bg, j.ID, state, detail); err != nil {
			log.Printf("recording result for %s: %v", j.Name(), err)
		}
	}
}

// RunDaemon runs the download worker and its local control API.
func RunDaemon(ctx context.Context, d *download.Downloader, addr string, st store.Store, o download.Options) error {
	q := New(st)
	jobs, _ := st.List(ctx)
	active := 0
	for _, j := range jobs {
		if j.Active() {
			active++
		}
	}
	log.Printf("Queue: %d job(s), %d waiting", len(jobs), active)

	workerDone := make(chan struct{})
	go func() {
		q.Work(ctx, d, o)
		close(workerDone)
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /queue", func(w http.ResponseWriter, r *http.Request) {
		jobs, logs, err := q.Snapshot(r.Context())
		if err != nil {
			http.Error(w, "storage error", http.StatusInternalServerError)
			return
		}
		httpx.WriteJSON(w, map[string]any{"jobs": jobs, "log": logs})
	})
	mux.HandleFunc("POST /queue", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			http.Error(w, "expected JSON", http.StatusUnsupportedMediaType)
			return
		}
		var req struct {
			Items []Item `json:"items"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		for _, it := range req.Items {
			if !mangadex.UUIDRe.MatchString(it.ID) || len(it.ID) != 36 {
				http.Error(w, "invalid id", http.StatusBadRequest)
				return
			}
		}
		added, skipped, err := q.Enqueue(r.Context(), req.Items)
		if err != nil {
			http.Error(w, "storage error", http.StatusInternalServerError)
			return
		}
		log.Printf("Enqueued %d series (%d already queued)", added, len(skipped))
		httpx.WriteJSON(w, map[string]any{"added": added, "skipped": skipped})
	})
	mux.HandleFunc("POST /queue/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		ok, err := q.Cancel(r.Context(), r.PathValue("id"))
		if err != nil {
			http.Error(w, "storage error", http.StatusInternalServerError)
			return
		}
		if !ok {
			http.Error(w, "not queued or running", http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /queue/clear", func(w http.ResponseWriter, r *http.Request) {
		if err := q.ClearFinished(r.Context()); err != nil {
			http.Error(w, "storage error", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: httpx.LocalOnly(ln.Addr().String(), mux)}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		srv.Shutdown(shutdown)
	}()
	log.Printf("Downloader listening on http://%s (Ctrl-C to stop; the queue is kept)", ln.Addr())
	if err := srv.Serve(ln); err != http.ErrServerClosed {
		return err
	}
	// Let the running job record that it was interrupted before we exit.
	select {
	case <-workerDone:
	case <-time.After(10 * time.Second):
		log.Print("warning: worker did not stop in time")
	}
	return nil
}
