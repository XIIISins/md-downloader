package store

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// File keeps the queue in memory and persists it as a JSON file. It assumes a
// single process owns the file.
type File struct {
	mu   sync.Mutex
	path string
	jobs []*Job

	progress map[string]Expected // see file_progress.go
}

// OpenFile loads the queue at path; a missing or corrupt file is an empty queue.
func OpenFile(path string) *File {
	f := &File{path: path, progress: map[string]Expected{}}
	f.loadProgress()
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &f.jobs)
	}
	// A job still marked running was interrupted by a shutdown or crash.
	// Downloads skip finished files, so re-queueing it simply resumes it.
	for _, j := range f.jobs {
		if j.State == StateRunning {
			j.State, j.Detail = StateQueued, "resuming after restart"
		}
	}
	return f
}

// Path is where the queue is stored.
func (f *File) Path() string { return f.path }

func (f *File) saveLocked() {
	if err := os.MkdirAll(filepath.Dir(f.path), 0o755); err != nil {
		log.Printf("warning: saving queue: %v", err)
		return
	}
	b, err := json.MarshalIndent(f.jobs, "", "  ")
	if err != nil {
		return
	}
	tmp := f.path + ".part"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		log.Printf("warning: saving queue: %v", err)
		return
	}
	_ = os.Rename(tmp, f.path)
}

func (f *File) find(id string) (int, *Job) {
	for i, j := range f.jobs {
		if j.ID == id {
			return i, j
		}
	}
	return -1, nil
}

func (f *File) Enqueue(_ context.Context, items []Item) (added int, skipped []string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now()
	for _, it := range items {
		i, existing := f.find(it.ID)
		if existing != nil {
			if existing.Active() {
				skipped = append(skipped, it.ID)
				continue
			}
			f.jobs = append(f.jobs[:i], f.jobs[i+1:]...)
		}
		f.jobs = append(f.jobs, &Job{ID: it.ID, Title: it.Title, State: StateQueued, Added: now, Updated: now})
		added++
	}
	f.saveLocked()
	return added, skipped, nil
}

func (f *File) Cancel(_ context.Context, id string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, j := f.find(id)
	if j == nil || !j.Active() {
		return false, nil
	}
	j.State, j.Detail, j.Updated = StateCanceled, "canceled", time.Now()
	f.saveLocked()
	return true, nil
}

func (f *File) ClearFinished(_ context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	kept := f.jobs[:0]
	for _, j := range f.jobs {
		if j.Active() {
			kept = append(kept, j)
		}
	}
	f.jobs = kept
	f.saveLocked()
	return nil
}

func (f *File) List(_ context.Context) ([]Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	jobs := make([]Job, 0, len(f.jobs))
	for _, j := range f.jobs {
		jobs = append(jobs, *j)
	}
	return jobs, nil
}

func (f *File) Claim(_ context.Context) (*Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, j := range f.jobs {
		if j.State == StateQueued {
			j.State, j.Detail, j.Updated = StateRunning, "starting", time.Now()
			f.saveLocked()
			c := *j
			return &c, nil
		}
	}
	return nil, nil
}

// Update only touches memory: detail lines change often and are not worth a disk write.
func (f *File) Update(_ context.Context, id, detail string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, j := f.find(id); j != nil && j.State == StateRunning {
		j.Detail, j.Updated = detail, time.Now()
		return nil
	}
	return ErrNotRunning
}

func (f *File) Finish(_ context.Context, id, state, detail string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, j := f.find(id); j != nil {
		j.State, j.Detail, j.Updated = state, detail, time.Now()
		f.saveLocked()
	}
	return nil
}
