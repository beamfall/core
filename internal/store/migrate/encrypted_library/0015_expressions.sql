-- CORE-0769: an Encrypted Library's saved filters, smart Collections, saved
-- Stations and Channels live in its sealed catalog plane (ADR-0045), so their
-- names and filter, Source, seed and rules expressions never reach catalog.db.
-- Each table mirrors its catalog shape (0041, 0042+0051+0078, 0082, 0098) with
-- the same column order and affinities. library_id and profile_id carry no
-- foreign key because the library and profile tables stay in catalog.db.
CREATE TABLE saved_filter (
  id            TEXT    NOT NULL PRIMARY KEY,
  profile_id    TEXT    NOT NULL,
  library_id    TEXT    NOT NULL,
  name          TEXT    NOT NULL,
  filter_json   TEXT    NOT NULL DEFAULT '{}',
  pinned        INTEGER NOT NULL DEFAULT 0,
  created_at_ms INTEGER NOT NULL
);

CREATE INDEX saved_filter_profile_library_idx
  ON saved_filter(profile_id, library_id);

CREATE TABLE smart_collection (
  id                    TEXT    NOT NULL PRIMARY KEY,
  library_id            TEXT    NOT NULL,
  name                  TEXT    NOT NULL,
  source_expr           TEXT    NOT NULL DEFAULT '{}',
  created_at_ms         INTEGER NOT NULL,
  created_by_profile_id TEXT,
  sort_expr             TEXT    NOT NULL DEFAULT '{}'
);

CREATE INDEX smart_collection_library_idx
  ON smart_collection(library_id);

CREATE INDEX smart_collection_created_by_idx
  ON smart_collection(created_by_profile_id);

CREATE TABLE saved_station (
  id            TEXT    NOT NULL PRIMARY KEY,
  profile_id    TEXT    NOT NULL,
  library_id    TEXT    NOT NULL,
  name          TEXT    NOT NULL,
  seed_json     TEXT    NOT NULL DEFAULT '{}',
  rules_json    TEXT    NOT NULL DEFAULT '{}',
  tuning_json   TEXT    NOT NULL DEFAULT '{}',
  created_at_ms INTEGER NOT NULL
);

CREATE INDEX saved_station_profile_library_idx
  ON saved_station(profile_id, library_id);

CREATE TABLE channel (
  id              TEXT    NOT NULL PRIMARY KEY,
  library_id      TEXT    NOT NULL,
  profile_id      TEXT,
  name            TEXT    NOT NULL,
  source_expr     TEXT    NOT NULL DEFAULT '{}',
  ordering_policy TEXT    NOT NULL DEFAULT 'shuffle',
  repeat_policy   TEXT    NOT NULL DEFAULT 'off',
  created_at_ms   INTEGER NOT NULL
);

CREATE INDEX channel_library_profile_idx
  ON channel(library_id, profile_id);

-- Rows written to catalog.db before this migration are legacy plaintext. The
-- next unseal moves them into the tables above and then clears this key; a
-- failed move leaves it set so the following unseal retries.
INSERT OR IGNORE INTO encrypted_library_metadata (key, value)
  VALUES ('legacy_expression_evacuation', 'pending');
