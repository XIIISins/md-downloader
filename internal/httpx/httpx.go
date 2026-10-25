// Package httpx holds small HTTP helpers shared by the web UI and the daemon.
package httpx

import (
	"encoding/json"
	"net"
	"net/http"
)

// LocalOnly rejects requests whose Host header isn't our own address, which
// blocks DNS-rebinding attacks against the local server.
func LocalOnly(listen string, next http.Handler) http.Handler {
	_, port, _ := net.SplitHostPort(listen)
	ok := map[string]bool{
		"127.0.0.1:" + port: true,
		"localhost:" + port: true,
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Health probes come from the kubelet with the pod IP as Host.
		if !ok[r.Host] && r.URL.Path != "/healthz" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// WriteJSON writes v as a JSON response.
func WriteJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// Truncate returns b as a string, cut to n bytes with "..." appended if longer.
func Truncate(b []byte, n int) string {
	if len(b) > n {
		return string(b[:n]) + "..."
	}
	return string(b)
}
