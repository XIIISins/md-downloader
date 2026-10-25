package mangadex

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"time"
)

const (
	reportURL   = "https://api.mangadex.network/report"
	reportPause = 10 * time.Minute
)

// DisableReports turns download reporting off, so tests never send fake URLs
// to MangaDex.
func (c *Client) DisableReports() {
	c.reportOff.Store(time.Now().Add(100 * 365 * 24 * time.Hour).UnixNano())
}

// Report tells the MangaDex network about a download result. It is best effort
// and runs in the background so a slow or dead endpoint never stalls downloads.
// After a few consecutive failures it pauses reporting for reportPause.
func (c *Client) Report(u string, success bool, size int, d time.Duration, cached bool) {
	if time.Now().UnixNano() < c.reportOff.Load() {
		return
	}
	select {
	case c.reportSem <- struct{}{}:
	default:
		return // too many reports in flight; drop this one
	}
	payload, _ := json.Marshal(map[string]any{
		"url":      u,
		"success":  success,
		"bytes":    size,
		"duration": d.Milliseconds(),
		"cached":   cached,
	})
	go func() {
		defer func() { <-c.reportSem }()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, reportURL, bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", UserAgent)
		resp, err := c.HTTP.Do(req)
		if err == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
		if err != nil || resp.StatusCode >= 500 {
			if c.reportFails.Add(1) >= 3 {
				c.reportFails.Store(0)
				c.reportOff.Store(time.Now().Add(reportPause).UnixNano())
				log.Printf("MangaDex report endpoint is failing; pausing reports for %s", reportPause)
			}
			return
		}
		c.reportFails.Store(0)
	}()
}
