// Command md-downloader downloads manga from MangaDex as CBZ files for offline
// reading. It can fetch one series, serve a web UI over your MangaDex library,
// or run the download daemon that works through the UI's queue.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/xiiisins/md-downloader/internal/download"
	"github.com/xiiisins/md-downloader/internal/library"
	"github.com/xiiisins/md-downloader/internal/mangadex"
	"github.com/xiiisins/md-downloader/internal/queue"
	"github.com/xiiisins/md-downloader/internal/server"
)

func main() {
	var (
		target     = flag.String("manga", "", "MangaDex title URL or UUID")
		libraryUI  = flag.Bool("library", false, "pick titles from your MangaDex library in a local web UI")
		status     = flag.String("status", "reading", "library status to list: reading, on_hold, plan_to_read, dropped, re_reading, completed, all")
		tokenFile  = flag.String("token-file", "personal-api-token", "file with id:/secret: (and optionally username:/password:) lines")
		addr       = flag.String("addr", "127.0.0.1:8484", "listen address for -library UI")
		cacheDir   = flag.String("cache-dir", library.DefaultCacheDir(), "where -library keeps its metadata and cover cache")
		daemon     = flag.Bool("daemon", false, "run the downloader: works through a persistent queue that -library fills")
		daemonAddr = flag.String("daemon-addr", "127.0.0.1:8485", "address the downloader daemon listens on (and -library connects to)")
		stateDir   = flag.String("state-dir", library.DefaultStateDir(), "where the daemon keeps its queue")
		staticDir  = flag.String("static", "", "serve the UI files from this directory instead of the embedded copy (for development)")
		refresh    = flag.Bool("refresh", false, "ignore cached library data and re-fetch everything")
		lang       = flag.String("lang", "en", "translated language code")
		out        = flag.String("out", "downloads", "output directory")
		format     = flag.String("format", "cbz", "output format: cbz or dir")
		dataSave   = flag.Bool("data-saver", false, "download compressed images")
		workers    = flag.Int("workers", 4, "concurrent page downloads per chapter")
		from       = flag.Float64("from", -1, "first chapter number to download")
		to         = flag.Float64("to", -1, "last chapter number to download")
		ratings    = flag.String("ratings", "safe,suggestive", "content ratings: safe,suggestive,erotica,pornographic")
	)
	flag.Parse()

	id := mangadex.UUIDRe.FindString(*target)
	if id == "" && !*libraryUI && !*daemon {
		fmt.Fprintln(os.Stderr, "usage: mangadex-scraper (-manga <url-or-uuid> | -library | -daemon) [flags]")
		flag.PrintDefaults()
		os.Exit(2)
	}
	if *format != "cbz" && *format != "dir" {
		log.Fatalf("invalid -format %q (want cbz or dir)", *format)
	}

	ctx, stop := signalContext()
	defer stop()

	c := mangadex.NewClient(&http.Client{Timeout: 60 * time.Second})
	d := &download.Downloader{API: c, DataSave: *dataSave, Workers: max(1, *workers)}
	opts := download.Options{
		Lang:    *lang,
		Out:     *out,
		Format:  *format,
		Ratings: strings.Split(*ratings, ","),
		From:    *from,
		To:      *to,
	}

	if *daemon {
		opts.From, opts.To = -1, -1 // chapter ranges only make sense for a single -manga run
		if err := queue.RunDaemon(ctx, d, *daemonAddr, *stateDir, opts); err != nil {
			log.Fatal(err)
		}
		return
	}

	if *libraryUI {
		creds, err := mangadex.LoadCredentials(*tokenFile)
		if err != nil {
			log.Fatalf("credentials: %v", err)
		}
		c.Auth = mangadex.NewSession(c.HTTP, creds)
		if err := server.Run(ctx, c, *status, *addr, *cacheDir, *daemonAddr, *refresh, *staticDir, opts); err != nil {
			log.Fatal(err)
		}
		return
	}

	t, err := d.SyncManga(ctx, id, opts, log.Printf)
	if err != nil {
		log.Fatal(err)
	}
	if t.Failed > 0 {
		os.Exit(1)
	}
}

func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}
