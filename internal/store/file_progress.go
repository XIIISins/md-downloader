package store

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sort"
)

var _ Progress = (*File)(nil)

// progressPath is where the file backend keeps Progress, next to the queue.
func (f *File) progressPath() string { return filepath.Join(filepath.Dir(f.path), "progress.json") }

func (f *File) loadProgress() {
	if b, err := os.ReadFile(f.progressPath()); err == nil {
		_ = json.Unmarshal(b, &f.progress)
	}
}

func (f *File) saveProgressLocked() {
	if err := os.MkdirAll(filepath.Dir(f.path), 0o755); err != nil {
		log.Printf("warning: saving progress: %v", err)
		return
	}
	b, err := json.Marshal(f.progress)
	if err != nil {
		return
	}
	tmp := f.progressPath() + ".part"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		log.Printf("warning: saving progress: %v", err)
		return
	}
	_ = os.Rename(tmp, f.progressPath())
}

// SetExpected implements Progress.
func (f *File) SetExpected(_ context.Context, items []Expected) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	next := make(map[string]Expected, len(items))
	for _, it := range items {
		it.Downloaded = f.progress[it.ID].Downloaded
		next[it.ID] = it
	}
	f.progress = next
	f.saveProgressLocked()
	return nil
}

// Expected implements Progress.
func (f *File) Expected(context.Context) ([]Expected, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Expected, 0, len(f.progress))
	for _, e := range f.progress {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// SetDownloaded implements Progress.
func (f *File) SetDownloaded(_ context.Context, id string, n int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if e, ok := f.progress[id]; ok {
		e.Downloaded = n
		f.progress[id] = e
		f.saveProgressLocked()
	}
	return nil
}

// Downloaded implements Progress.
func (f *File) Downloaded(context.Context) (map[string]int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]int, len(f.progress))
	for id, e := range f.progress {
		out[id] = e.Downloaded
	}
	return out, nil
}
