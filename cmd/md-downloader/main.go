// Command md-downloader downloads manga from MangaDex as CBZ files for offline
// reading. It can fetch one series, serve a web UI over your MangaDex library,
// or run the download daemon that works through the UI's queue.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/xiiisins/md-downloader/internal/download"
	"github.com/xiiisins/md-downloader/internal/library"
	"github.com/xiiisins/md-downloader/internal/mangadex"
	"github.com/xiiisins/md-downloader/internal/queue"
	"github.com/xiiisins/md-downloader/internal/server"
	"github.com/xiiisins/md-downloader/internal/store"
	"github.com/xiiisins/md-downloader/internal/store/pg"
	"github.com/xiiisins/md-downloader/internal/syncer"
)

func main() {
	var (
		target      = flag.String("manga", "", "MangaDex title URL or UUID")
		libraryUI   = flag.Bool("library", false, "pick titles from your MangaDex library in a local web UI")
		status      = flag.String("status", "reading", "library status to list: reading, on_hold, plan_to_read, dropped, re_reading, completed, all")
		tokenFile   = flag.String("token-file", "personal-api-token", "file with id:/secret: (and optionally username:/password:) lines")
		addr        = flag.String("addr", "127.0.0.1:8484", "listen address for -library UI")
		cacheDir    = flag.String("cache-dir", library.DefaultCacheDir(), "where -library keeps its metadata and cover cache")
		daemon      = flag.Bool("daemon", false, "run the downloader: works through a persistent queue that -library fills")
		daemonAddr  = flag.String("daemon-addr", "127.0.0.1:8485", "address the downloader daemon listens on (and -library connects to)")
		stateDir    = flag.String("state-dir", library.DefaultStateDir(), "where the daemon keeps its queue")
		databaseURL = flag.String("database-url", os.Getenv("DATABASE_URL"), "Postgres URL for the queue (default $DATABASE_URL); empty keeps the queue in a file under -state-dir")
		apiMode     = flag.Bool("api", false, "serve the web API from the shared database (needs -database-url); run behind a reverse proxy")
		syncMode    = flag.Bool("sync", false, "copy your MangaDex library into the shared database on a schedule (needs -database-url)")
		syncEvery   = flag.Duration("sync-interval", time.Hour, "time between library syncs for -sync")
		syncOnce    = flag.Bool("once", false, "with -sync: sync once and exit")
		staticDir   = flag.String("static", "", "serve the UI files from this directory instead of the embedded copy (for development)")
		refresh     = flag.Bool("refresh", false, "ignore cached library data and re-fetch everything")
		lang        = flag.String("lang", "en", "translated language code")
		out         = flag.String("out", "downloads", "output directory")
		format      = flag.String("format", "cbz", "output format: cbz or dir")
		dataSave    = flag.Bool("data-saver", false, "download compressed images")
		workers     = flag.Int("workers", 4, "concurrent page downloads per chapter")
		from        = flag.Float64("from", -1, "first chapter number to download")
		to          = flag.Float64("to", -1, "last chapter number to download")
		ratings     = flag.String("ratings", "safe,suggestive", "content ratings: safe,suggestive,erotica,pornographic")
	)
	flag.Parse()

	id := mangadex.UUIDRe.FindString(*target)
	if id == "" && !*libraryUI && !*daemon && !*apiMode && !*syncMode {
		fmt.Fprintln(os.Stderr, "usage: md-downloader (-manga <url-or-uuid> | -library | -daemon | -api | -sync) [flags]")
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
		var st store.Store
		if *databaseURL != "" {
			ps := openDB(ctx, *databaseURL)
			defer ps.Close()
			st = ps
			log.Print("Queue state in Postgres")
		} else {
			fs := store.OpenFile(filepath.Join(*stateDir, "queue.json"))
			log.Printf("Queue state in %s", fs.Path())
			st = fs
		}
		if err := queue.RunDaemon(ctx, d, *daemonAddr, st, opts); err != nil {
			log.Fatal(err)
		}
		return
	}

	if *apiMode {
		if *databaseURL == "" {
			log.Fatal("-api needs -database-url (or $DATABASE_URL)")
		}
		ps := openDB(ctx, *databaseURL)
		defer ps.Close()
		s := server.New(server.Config{
			Client: c,
			Items: func(ctx context.Context) ([]mangadex.Manga, error) {
				items, _, err := ps.Library(ctx)
				return items, err
			},
			Backend:   server.StoreBackend{Store: ps},
			CoverDir:  filepath.Join(*cacheDir, "covers"),
			StaticDir: *staticDir,
		})
		ln, err := net.Listen("tcp", *addr)
		if err != nil {
			log.Fatal(err)
		}
		log.Printf("API listening on http://%s", ln.Addr())
		if err := server.Serve(ctx, ln, s.Handler()); err != nil {
			log.Fatal(err)
		}
		return
	}

	if *syncMode {
		if *databaseURL == "" {
			log.Fatal("-sync needs -database-url (or $DATABASE_URL)")
		}
		creds, err := mangadex.LoadCredentials(*tokenFile)
		if err != nil {
			log.Fatalf("credentials: %v", err)
		}
		c.Auth = mangadex.NewSession(c.HTTP, creds)
		ps := openDB(ctx, *databaseURL)
		defer ps.Close()
		err = syncer.Run(ctx, syncer.Config{
			Client:   c,
			Library:  ps,
			Cache:    ps.Blob("library_cache"),
			Status:   *status,
			Lang:     opts.Lang,
			Ratings:  opts.Ratings,
			Interval: *syncEvery,
			Once:     *syncOnce,
			Refresh:  *refresh,
		})
		if err != nil {
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
		if err := server.RunLocal(ctx, c, *status, *addr, *cacheDir, *daemonAddr, *refresh, *staticDir, opts); err != nil {
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

// openDB connects to the shared database, identifying this process as a job owner.
func openDB(ctx context.Context, url string) *pg.Store {
	host, _ := os.Hostname()
	ps, err := pg.Open(ctx, url, fmt.Sprintf("%s/%d", host, os.Getpid()))
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	return ps
}

func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}
