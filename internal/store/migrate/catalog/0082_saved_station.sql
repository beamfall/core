-- Saved Stations (ADR-0098 area 5 / MUSIC-5): a named, per-Profile, per-Library
-- saved Station. A Station is Beamfall Music PRODUCT LANGUAGE over a generated
-- Play Queue, NEVER a Core schema type (ADR-0098 invariant #1/#2) — this table
-- stores only the Profile-owned generation inputs and product-behaviour
-- provenance: seed, rules, tuning. It deliberately does NOT store the generated
-- track order; saving an exact track order remains a Playlist / ordered
-- Collection. seed_json / rules_json / tuning_json are opaque client expressions
-- the server stores and returns verbatim (same posture as saved_filter.filter_json).
-- Cascade-deletes when the owning Profile or Library is removed; Profile-owned
-- data participates in My Data export/delete (ADR-0036).
CREATE TABLE IF NOT EXISTS saved_station (
  id            TEXT    NOT NULL PRIMARY KEY,
  profile_id    TEXT    NOT NULL REFERENCES profile(id)  ON DELETE CASCADE,
  library_id    TEXT    NOT NULL REFERENCES library(id)  ON DELETE CASCADE,
  name          TEXT    NOT NULL,
  seed_json     TEXT    NOT NULL DEFAULT '{}',
  rules_json    TEXT    NOT NULL DEFAULT '{}',
  tuning_json   TEXT    NOT NULL DEFAULT '{}',
  created_at_ms INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS saved_station_profile_library_idx
  ON saved_station(profile_id, library_id);
