package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/xiiisins/md-downloader/internal/download"
	"github.com/xiiisins/md-downloader/internal/httpx"
	"github.com/xiiisins/md-downloader/internal/mangadex"
)

// Job states. queued and running are "active": a series in either state can't
// be queued again.
const (
	stateQueued   = "queued"
	stateRunning  = "running"
	stateDone     = "done"
	stateFailed   = "failed"
	stateCanceled = "canceled"
)

type Job struct {
	ID      string    `json:"id"`
	Title   string    `json:"title"`
	State   string    `json:"state"`
	Detail  string    `json:"detail"`
	Added   time.Time `json:"added"`
	Updated time.Time `json:"updated"`
}

func (j *Job) active() bool { return j.State == stateQueued || j.State == stateRunning }

func (j *Job) name() string {
	if j.Title != "" {
		return j.Title
	}
	return j.ID
}

type Item struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// queue is a persistent, ordered list of series to download. The daemon is its
// only writer, so a plain JSON file is enough.
type Queue struct {
	mu   sync.Mutex
	path string
	jobs []*Job
	logs []string
	wake chan struct{}

	curID        string
	cancelCur    context.CancelFunc
	userCanceled bool
}

func Load(path string) *Queue {
	q := &Queue{path: path, wake: make(chan struct{}, 1)}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &q.jobs)
	}
	// A job still marked running was interrupted by a shutdown or crash.
	// Downloads skip finished files, so re-queueing it simply resumes it.
	for _, j := range q.jobs {
		if j.State == stateRunning {
			j.State, j.Detail = stateQueued, "resuming after restart"
		}
	}
	return q
}

func (q *Queue) saveLocked() {
	if err := os.MkdirAll(filepath.Dir(q.path), 0o755); err != nil {
		log.Printf("warning: saving queue: %v", err)
		return
	}
	b, err := json.MarshalIndent(q.jobs, "", "  ")
	if err != nil {
		return
	}
	tmp := q.path + ".part"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		log.Printf("warning: saving queue: %v", err)
		return
	}
	_ = os.Rename(tmp, q.path)
}

func (q *Queue) find(id string) (int, *Job) {
	for i, j := range q.jobs {
		if j.ID == id {
			return i, j
		}
	}
	return -1, nil
}

// enqueue adds series to the end of the queue. Series that are already queued
// or running are skipped and returned.
func (q *Queue) enqueue(items []Item) (added int, skipped []string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	now := time.Now()
	for _, it := range items {
		i, existing := q.find(it.ID)
		if existing != nil {
			if existing.active() {
				skipped = append(skipped, it.ID)
				continue
			}
			// Finished before: drop the old record so it re-enters at the back.
			q.jobs = append(q.jobs[:i], q.jobs[i+1:]...)
		}
		q.jobs = append(q.jobs, &Job{ID: it.ID, Title: it.Title, State: stateQueued, Added: now, Updated: now})
		added++
	}
	q.saveLocked()
	select {
	case q.wake <- struct{}{}:
	default:
	}
	return added, skipped
}

// cancel stops a queued or running series.
func (q *Queue) cancel(id string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	_, j := q.find(id)
	if j == nil || !j.active() {
		return false
	}
	if j.State == stateRunning && q.curID == id {
		q.userCanceled = true
		q.cancelCur()
		return true // work() records the final state
	}
	j.State, j.Detail, j.Updated = stateCanceled, "canceled", time.Now()
	q.saveLocked()
	return true
}

func (q *Queue) clearFinished() {
	q.mu.Lock()
	defer q.mu.Unlock()
	kept := q.jobs[:0]
	for _, j := range q.jobs {
		if j.active() {
			kept = append(kept, j)
		}
	}
	q.jobs = kept
	q.saveLocked()
}

func (q *Queue) snapshot() (jobs []Job, logs []string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, j := range q.jobs {
		jobs = append(jobs, *j)
	}
	return jobs, append([]string(nil), q.logs...)
}

// claim marks the first queued job as running.
func (q *Queue) claim() *Job {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, j := range q.jobs {
		if j.State == stateQueued {
			j.State, j.Detail, j.Updated = stateRunning, "starting", time.Now()
			q.saveLocked()
			return j
		}
	}
	return nil
}

func (q *Queue) note(j *Job, line string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	j.Detail, j.Updated = line, time.Now()
	q.logs = append(q.logs, j.name()+": "+line)
	if len(q.logs) > 200 {
		q.logs = q.logs[len(q.logs)-200:]
	}
}

// Work runs queued series one at a time until ctx is canceled.
func (q *Queue) Work(ctx context.Context, d *download.Downloader, o download.Options) {
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

		j := q.claim()
		if j == nil {
			select {
			case <-q.wake:
			case <-ctx.Done():
			}
			continue
		}

		jctx, cancel := context.WithCancel(ctx)
		q.mu.Lock()
		q.curID, q.cancelCur, q.userCanceled = j.ID, cancel, false
		q.mu.Unlock()

		logf := func(format string, args ...any) {
			line := fmt.Sprintf(format, args...)
			log.Printf("%s: %s", j.name(), line)
			q.note(j, line)
		}
		t, err := d.SyncManga(jctx, j.ID, o, logf)
		cancel()

		q.mu.Lock()
		canceled := q.userCanceled
		q.curID, q.cancelCur, q.userCanceled = "", nil, false
		switch {
		case ctx.Err() != nil && !canceled:
			j.State, j.Detail = stateQueued, "interrupted; will resume"
		case canceled:
			j.State, j.Detail = stateCanceled, "canceled"
		case errors.Is(err, download.ErrOutputUnavailable):
			j.State, j.Detail = stateQueued, "waiting for output directory"
		case err != nil:
			j.State, j.Detail = stateFailed, err.Error()
		case t.Failed > 0:
			j.State, j.Detail = stateFailed, t.String()
		default:
			j.State, j.Detail = stateDone, t.String()
		}
		j.Updated = time.Now()
		q.saveLocked()
		q.mu.Unlock()
	}
}

// RunDaemon runs the download worker and its local control API.
func RunDaemon(ctx context.Context, d *download.Downloader, addr, stateDir string, o download.Options) error {
	q := Load(filepath.Join(stateDir, "queue.json"))
	active := 0
	for _, j := range q.jobs {
		if j.active() {
			active++
		}
	}
	log.Printf("Queue: %d job(s), %d waiting (state in %s)", len(q.jobs), active, q.path)

	workerDone := make(chan struct{})
	go func() {
		q.Work(ctx, d, o)
		close(workerDone)
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /queue", func(w http.ResponseWriter, r *http.Request) {
		jobs, logs := q.snapshot()
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
		added, skipped := q.enqueue(req.Items)
		log.Printf("Enqueued %d series (%d already queued)", added, len(skipped))
		httpx.WriteJSON(w, map[string]any{"added": added, "skipped": skipped})
	})
	mux.HandleFunc("POST /queue/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		if !q.cancel(r.PathValue("id")) {
			http.Error(w, "not queued or running", http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /queue/clear", func(w http.ResponseWriter, r *http.Request) {
		q.clearFinished()
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
