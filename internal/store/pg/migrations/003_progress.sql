-- What each library series should have on disk, and how much of it does.
-- Kept apart from library_items so GET /api/library stays small.
CREATE TABLE library_chapters (
    series_id  text PRIMARY KEY,
    title      text NOT NULL,
    keys       text[] NOT NULL DEFAULT '{}',
    downloaded integer NOT NULL DEFAULT 0
);
