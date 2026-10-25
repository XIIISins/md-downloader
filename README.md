# md-downloader

Download the manga you read on [MangaDex](https://mangadex.org) for offline reading, as CBZ files that
work with Kavita, Komga and most other readers. It uses MangaDex's public API.

It is a personal tool for offline access to series you already follow. It is not affiliated with MangaDex.
Please respect the creators and groups behind the series, and MangaDex's terms of service.

## Features

- **Downloads whole series** as one CBZ per chapter (or plain image folders), at the documented rate limits.
  Reruns skip what you already have, so it is safe to interrupt and resume.
- **Queue daemon** (`-daemon`): works through a persistent queue one series at a time, survives restarts,
  and pauses when the output folder disappears (for example a dropped network mount) instead of failing series.
- **Library web UI** (`-library`): browse your MangaDex "Reading" list with covers, summaries, tags, ratings and
  start years. Filter by tag (include or exclude), demographic, year range and title; sort several ways;
  mark series Y or N and queue them. Series that are queued or running are locked so you cannot queue them twice.
- Only lists series that have downloadable chapters in your language, so licensed or taken-down series drop out.
- Picks a hosted copy when a chapter has both an official external link and a hosted upload.
- Retries failed pages against a fresh MangaDex image server.

## Install

Requires Go 1.22 or newer.

```sh
go build -o md-downloader .
```

## Credentials

Downloading a single series needs no account. The library UI does.

1. In MangaDex, open Settings, then API Clients, and create a personal client.
2. Copy `personal-api-token.example` to `personal-api-token` and fill it in:

   ```
   id:personal-client-...
   secret:...
   username:your-mangadex-username
   password:your-mangadex-password
   ```

3. Restrict it: `chmod 600 personal-api-token`.

You can instead set `MD_CLIENT_ID`, `MD_CLIENT_SECRET`, `MD_USERNAME` and `MD_PASSWORD`, which override the file.
The file is git-ignored. Never commit credentials.

## Usage

Download one series (a title URL or its UUID):

```sh
./md-downloader -manga https://mangadex.org/title/<uuid>/<slug> -out ~/Manga
./md-downloader -manga <uuid> -from 10 -to 20 -data-saver -format dir
```

Run the downloader and the web UI, normally as two processes:

```sh
./md-downloader -daemon -out ~/Manga -workers 8   # terminal 1: processes the queue
./md-downloader -library                           # terminal 2: web UI at http://127.0.0.1:8484
```

Keep the daemon in its own terminal or run it in the background. Do not suspend it with Ctrl-Z: it keeps
accepting connections but stops answering. Stop it with Ctrl-C; the queue is kept and interrupted series resume.

### Flags

| Flag | Default | Meaning |
|------|---------|---------|
| `-manga` | | Title URL or UUID to download |
| `-library` | off | Start the web UI for your MangaDex library |
| `-daemon` | off | Run the queue daemon |
| `-status` | `reading` | Library list to show: `reading`, `on_hold`, `plan_to_read`, `dropped`, `re_reading`, `completed`, `all` |
| `-lang` | `en` | Translated language code |
| `-out` | `downloads` | Output directory (the daemon requires it to exist, or its parent to) |
| `-format` | `cbz` | `cbz` or `dir` |
| `-data-saver` | off | Compressed images, much smaller |
| `-workers` | `4` | Concurrent page downloads per chapter |
| `-from`, `-to` | | Chapter number range (single-series mode) |
| `-ratings` | `safe,suggestive` | Content ratings: `safe,suggestive,erotica,pornographic` |
| `-addr` | `127.0.0.1:8484` | Web UI listen address |
| `-daemon-addr` | `127.0.0.1:8485` | Daemon listen address, and where the web UI finds it |
| `-token-file` | `personal-api-token` | Credentials file |
| `-cache-dir` | user cache dir | Library metadata and cover cache |
| `-state-dir` | user config dir | Where the daemon keeps its queue |
| `-refresh` | off | Ignore cached library data |

Output looks like `<out>/<Series title>/ch0001.cbz`. Chapter numbers are zero-padded so they sort correctly.

## How it behaves

- **Rate limits:** API calls are spaced to stay under MangaDex's limits, chapters are fetched one at a time,
  and `429` responses are honoured.
- **Caching:** library titles, tags, ratings and covers are cached on disk (titles for 24 hours, chapter
  availability for 6 hours), so relaunching the UI is fast. Use `-refresh` to force a reload.
- **Reports:** after each image it tells the MangaDex network how the download went, as they ask. If that
  endpoint is failing, reporting pauses for ten minutes and never slows downloads.
- **Local only:** the web UI and daemon bind to `127.0.0.1` and reject requests with an unexpected `Host` header.

## Development

```sh
git config core.hooksPath .githooks   # enable the secret-scanning pre-commit hook
go vet ./... && go test -race ./...
```

The pre-commit hook blocks credential files, any value from your local `personal-api-token`, and gitleaks
findings. CI runs gofmt, vet, build, tests and a gitleaks scan of the full history. Changes go through pull
requests into `main`, which are squash-merged.

## License

MIT, see [LICENSE](LICENSE).
