-- SavedFilters (ADR-0011 §10): named, per-Profile, per-Library declarative
-- filters that operators can pin into Layouts. filter_json is an opaque client
-- expression; the server stores and returns it without interpretation. Cascade-
-- deletes when the owning Profile or Library is removed.
CREATE TABLE IF NOT EXISTS saved_filter (
  id            TEXT    NOT NULL PRIMARY KEY,
  profile_id    TEXT    NOT NULL REFERENCES profile(id)  ON DELETE CASCADE,
  library_id    TEXT    NOT NULL REFERENCES library(id)  ON DELETE CASCADE,
  name          TEXT    NOT NULL,
  filter_json   TEXT    NOT NULL DEFAULT '{}',
  pinned        INTEGER NOT NULL DEFAULT 0,
  created_at_ms INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS saved_filter_profile_library_idx
  ON saved_filter(profile_id, library_id);
