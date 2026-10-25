// Package mangadex is a small client for the parts of the MangaDex API this
// tool uses: chapter feeds, image servers, library and series metadata.
//
// It follows the documented flow: list chapters via the feed endpoint, resolve
// an image server via /at-home/server, and report each download to the MangaDex
// network so they can monitor server health.
package mangadex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/xiiisins/md-downloader/internal/httpx"
	"github.com/xiiisins/md-downloader/internal/retry"
)

const (
	// DefaultBaseURL is the MangaDex API root.
	DefaultBaseURL = "https://api.mangadex.org"
	// UserAgent identifies this tool to MangaDex.
	UserAgent = "mangadex-scraper/1.0 (personal offline reader)"

	maxRetries = 4
)

// UUIDRe matches a MangaDex ID anywhere in a string, such as a title URL.
var UUIDRe = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)

// Limiter spaces calls at least `interval` apart.
type Limiter struct {
	mu       sync.Mutex
	interval time.Duration
	next     time.Time
}

// NewLimiter returns a Limiter that allows one call per interval.
func NewLimiter(interval time.Duration) *Limiter {
	return &Limiter{interval: interval}
}

// Wait blocks until the caller may proceed, or ctx is done.
func (l *Limiter) Wait(ctx context.Context) error {
	l.mu.Lock()
	now := time.Now()
	if l.next.Before(now) {
		l.next = now
	}
	at := l.next
	l.next = at.Add(l.interval)
	l.mu.Unlock()

	select {
	case <-time.After(time.Until(at)):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Client talks to the MangaDex API at its documented rate limits.
type Client struct {
	HTTP        *http.Client
	BaseURL     string   // API root; tests point it at a fake server
	APILimit    *Limiter // general API: ~5 req/s, stay under with 250ms
	AtHomeLimit *Limiter // /at-home/server: ~40 req/min, stay under with 1.6s
	Auth        *Session // nil unless logged in

	reportSem   chan struct{} // bounds in-flight download reports
	reportFails atomic.Int32  // consecutive report failures
	reportOff   atomic.Int64  // unix nanos until which reporting is paused
}

// NewClient returns a Client with MangaDex's default base URL and rate limits.
func NewClient(httpc *http.Client) *Client {
	return &Client{
		HTTP:        httpc,
		BaseURL:     DefaultBaseURL,
		APILimit:    NewLimiter(250 * time.Millisecond),
		AtHomeLimit: NewLimiter(1600 * time.Millisecond),
		reportSem:   make(chan struct{}, 8),
	}
}

// getJSON performs a rate-limited GET against the API, honouring 429s.
func (c *Client) getJSON(ctx context.Context, lim *Limiter, u string, out any) error {
	return c.doJSON(ctx, lim, u, out, false)
}

// getJSONAuth is getJSON with the user's bearer token attached.
func (c *Client) getJSONAuth(ctx context.Context, lim *Limiter, u string, out any) error {
	if c.Auth == nil {
		return errors.New("not logged in")
	}
	return c.doJSON(ctx, lim, u, out, true)
}

func (c *Client) doJSON(ctx context.Context, lim *Limiter, u string, out any, bearer bool) error {
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if err := lim.Wait(ctx); err != nil {
			return err
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		req.Header.Set("User-Agent", UserAgent)
		if bearer {
			tok, err := c.Auth.token(ctx)
			if err != nil {
				return err
			}
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		resp, err := c.HTTP.Do(req)
		if err != nil {
			lastErr = err
			retry.Sleep(ctx, retry.Backoff(attempt))
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		switch {
		case resp.StatusCode == http.StatusTooManyRequests:
			d := 5 * time.Second
			if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil {
				d = time.Duration(s) * time.Second
			}
			lastErr = errors.New("rate limited")
			retry.Sleep(ctx, d)
		case resp.StatusCode == http.StatusUnauthorized && bearer:
			lastErr = errors.New("unauthorized")
			c.Auth.invalidate()
		case resp.StatusCode >= 500:
			lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
			retry.Sleep(ctx, retry.Backoff(attempt))
		case resp.StatusCode != http.StatusOK:
			return fmt.Errorf("GET %s: HTTP %d: %s", u, resp.StatusCode, httpx.Truncate(body, 200))
		default:
			return json.Unmarshal(body, out)
		}
	}
	return fmt.Errorf("GET %s: %w", u, lastErr)
}
