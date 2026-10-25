package server

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/xiiisins/md-downloader/internal/download"
	"github.com/xiiisins/md-downloader/internal/httpx"
	"github.com/xiiisins/md-downloader/internal/library"
	"github.com/xiiisins/md-downloader/internal/mangadex"
)

// Serve serves h on ln until ctx is done.
func Serve(ctx context.Context, ln net.Listener, h http.Handler) error {
	srv := &http.Server{Handler: h}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		srv.Shutdown(shutdown)
	}()
	if err := srv.Serve(ln); err != http.ErrServerClosed {
		return err
	}
	return nil
}

// RunLocal runs the single-user library UI: it loads the library straight from
// MangaDex (c must be logged in), and forwards downloads to a local daemon.
func RunLocal(ctx context.Context, c *mangadex.Client, status, addr, cacheDir, daemonAddr string, refresh bool, staticDir string, opts download.Options) error {
	log.Printf("Logging in and loading library (%s)...", status)
	ids, err := c.LibraryStatuses(ctx, status)
	if err != nil {
		return fmt.Errorf("loading library: %w", err)
	}
	items, err := library.LoadItems(ctx, c, ids, opts.Lang, opts.Ratings, library.LoadCacheDir(cacheDir), refresh)
	if err != nil {
		return err
	}
	log.Printf("Loaded %d title(s)", len(items))

	backend := &DaemonBackend{Addr: daemonAddr, HTTP: c.HTTP}
	s := New(Config{
		Client:    c,
		Items:     func(context.Context) ([]mangadex.Manga, error) { return items, nil },
		Backend:   backend,
		CoverDir:  filepath.Join(cacheDir, "covers"),
		StaticDir: staticDir,
	})
	go s.PrefetchCovers(ctx)

	if _, _, err := backend.Snapshot(ctx); err != nil {
		log.Printf("Warning: downloader not reachable at %s. Start it with: ./md-downloader -daemon", daemonAddr)
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	page := "http://" + ln.Addr().String()
	log.Printf("Open %s (Ctrl-C to quit)", page)
	openBrowser(page)
	return Serve(ctx, ln, httpx.LocalOnly(ln.Addr().String(), s.Handler()))
}

func openBrowser(u string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", u)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	default:
		cmd = exec.Command("xdg-open", u)
	}
	_ = cmd.Start()
}
