// Package retry holds the sleep and backoff helpers shared by the network code.
package retry

import (
	"context"
	"time"
)

// Unit scales Backoff delays. Tests shrink it so retries run instantly.
var Unit = time.Second

// Sleep waits for d, or until ctx is done.
func Sleep(ctx context.Context, d time.Duration) {
	select {
	case <-time.After(d):
	case <-ctx.Done():
	}
}

// Backoff returns the exponential delay before retry number attempt (counting
// from 0): 1, 2, 4, 8 units.
func Backoff(attempt int) time.Duration {
	return time.Duration(1<<attempt) * Unit
}
