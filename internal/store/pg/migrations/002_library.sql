-- The synced MangaDex library, as served by the API.
CREATE TABLE library_items (
    series_id text PRIMARY KEY,
    data      jsonb NOT NULL,
    cover     text NOT NULL DEFAULT '',
    synced    timestamptz NOT NULL DEFAULT now()
);

-- Small named blobs, e.g. the sync worker's metadata cache.
CREATE TABLE blobs (
    name    text PRIMARY KEY,
    data    bytea NOT NULL,
    updated timestamptz NOT NULL DEFAULT now()
);
