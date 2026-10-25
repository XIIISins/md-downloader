# syntax=docker/dockerfile:1
# Static UI behind Caddy, plain HTTP on :8080 as uid 1000. Build from the repo
# root: docker build -f web/Dockerfile .
# Reads API_UPSTREAM (default md-downloader-api:8080) for /api/* and /cover/*.

FROM caddy:2.11-alpine@sha256:eb4d35b7452d1ab6171fbdbe8d9275b2301dad6dad92115d91e19fb9b24d510f
COPY Caddyfile /etc/caddy/Caddyfile
COPY web/index.html /srv/index.html
COPY web/static /srv/static
# Caddy writes autosave/state under these; keep them writable for uid 1000.
ENV XDG_CONFIG_HOME=/tmp/caddy/config \
    XDG_DATA_HOME=/tmp/caddy/data
RUN mkdir -p /tmp/caddy/config /tmp/caddy/data && chown -R 1000:1000 /tmp/caddy
USER 1000:1000
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s CMD ["wget", "-q", "-O", "/dev/null", "http://127.0.0.1:8080/healthz"]
