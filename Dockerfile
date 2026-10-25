# syntax=docker/dockerfile:1
# One image for every mode: pass the mode as args, e.g. `-daemon`, `-library`.
# Runs as uid 1000 with no shell; /data is the working directory and holds the
# download output, cache and queue state, so mount a volume there.

FROM golang:1.25-alpine@sha256:1ae0735f00daffa3aaf1363a5184c0d2dc55c78e3db4ec70241cdac97bf84b59 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY web ./web
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/md-downloader ./cmd/md-downloader
# The final image has no shell, so create the writable data dir here.
RUN mkdir /out/data && chown 1000:1000 /out/data

FROM gcr.io/distroless/static-debian13:nonroot@sha256:18f4cc26c9ffa7f20a359298be7deadac5c607c8e03a02b8119f1a5fc2f82c99
COPY --from=build /out/md-downloader /md-downloader
COPY --from=build --chown=1000:1000 /out/data /data
ENV HOME=/data \
    XDG_CACHE_HOME=/data/cache \
    XDG_CONFIG_HOME=/data/state
WORKDIR /data
USER 1000:1000
ENTRYPOINT ["/md-downloader"]
