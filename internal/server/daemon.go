package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/xiiisins/md-downloader/internal/httpx"
	"github.com/xiiisins/md-downloader/internal/store"
)

// DaemonBackend talks to a downloader daemon's local control API.
type DaemonBackend struct {
	Addr string // host:port
	HTTP *http.Client
}

var _ Backend = (*DaemonBackend)(nil)

func (d *DaemonBackend) do(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	// The daemon is local and answers instantly; don't wait out the 60s client timeout.
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, "http://"+d.Addr+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := d.HTTP.Do(req)
	if err != nil {
		msg := "Downloader not reachable. Start it with: ./md-downloader -daemon"
		if os.IsTimeout(err) {
			// The port is open but nothing answers: typically a daemon suspended with Ctrl-Z.
			msg = "Downloader is not responding. If you suspended it (Ctrl-Z), resume it with `bg` or `kill -CONT <pid>`."
		}
		return nil, fmt.Errorf("%w", &userErr{&UserError{msg}, err})
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return b, fmt.Errorf("downloader: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(httpx.Truncate(b, 200)))
	}
	return b, nil
}

// userErr pairs a UI message with the underlying error for the log.
type userErr struct {
	*UserError
	cause error
}

func (e *userErr) Unwrap() []error { return []error{e.UserError, e.cause} }

func (d *DaemonBackend) Enqueue(ctx context.Context, items []store.Item) (int, []string, error) {
	body, _ := json.Marshal(map[string]any{"items": items})
	b, err := d.do(ctx, http.MethodPost, "/queue", body)
	if err != nil {
		return 0, nil, err
	}
	var r struct {
		Added   int      `json:"added"`
		Skipped []string `json:"skipped"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return 0, nil, err
	}
	return r.Added, r.Skipped, nil
}

func (d *DaemonBackend) Cancel(ctx context.Context, id string) (bool, error) {
	_, err := d.do(ctx, http.MethodPost, "/queue/"+id+"/cancel", nil)
	return err == nil, err
}

func (d *DaemonBackend) ClearFinished(ctx context.Context) error {
	_, err := d.do(ctx, http.MethodPost, "/queue/clear", nil)
	return err
}

func (d *DaemonBackend) Snapshot(ctx context.Context) ([]store.Job, []string, error) {
	b, err := d.do(ctx, http.MethodGet, "/queue", nil)
	if err != nil {
		return nil, nil, err
	}
	var r struct {
		Jobs []store.Job `json:"jobs"`
		Log  []string    `json:"log"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, nil, err
	}
	return r.Jobs, r.Log, nil
}

// StoreBackend uses a shared store directly; the daemon notices the changes by
// polling the same store.
type StoreBackend struct{ store.Store }

var _ Backend = StoreBackend{}

// Snapshot lists the jobs. Log lines live in the daemon's memory, so there are none.
func (b StoreBackend) Snapshot(ctx context.Context) ([]store.Job, []string, error) {
	jobs, err := b.List(ctx)
	if jobs == nil {
		jobs = []store.Job{}
	}
	return jobs, []string{}, err
}
