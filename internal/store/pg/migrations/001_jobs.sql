CREATE TABLE jobs (
    series_id   text PRIMARY KEY,
    title       text NOT NULL DEFAULT '',
    state       text NOT NULL,
    detail      text NOT NULL DEFAULT '',
    seq         bigint GENERATED ALWAYS AS IDENTITY,
    added       timestamptz NOT NULL DEFAULT now(),
    updated     timestamptz NOT NULL DEFAULT now(),
    lease_owner text,
    lease_until timestamptz
);

CREATE INDEX jobs_queued_idx ON jobs (seq) WHERE state = 'queued';
