package main

import (
	"bytes"
	"context"
	"io"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-time.After(d):
	case <-ctx.Done():
	}
}

// backoffUnit scales retry delays; tests shrink it.
var backoffUnit = time.Second

func backoff(attempt int) time.Duration {
	return time.Duration(1<<attempt) * backoffUnit
}

func truncate(b []byte, n int) string {
	if len(b) > n {
		return string(b[:n]) + "..."
	}
	return string(b)
}

func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }
