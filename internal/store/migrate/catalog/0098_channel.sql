-- CHAN-1 (ADR-0158 Library Channels): a named virtual-linear playback object.
-- A Channel is a GENERIC STRUCTURAL object, never a Core schema entity
-- (invariants #1/#2) — it wraps a content source (a smart-collection reference
-- or an inline query, ADR-0067) plus an ordering and repeat policy, and compiles
-- to an ordinary Play Queue on tune (Decision 3). It stores only the source
-- expression and policy inputs; it deliberately does NOT persist a generated
-- track order (that materializes at tune time, mirroring saved_station's
-- posture). source_expr is opaque client JSON the server stores and returns
-- verbatim (same posture as smart_collection.source_expr / saved_filter
-- .filter_json). ordering_policy is v1's shuffle|chronological, the column open
-- for `marathon` as a later extension (resolved question 2). profile_id NULL is
-- a server-shared Channel; a non-NULL profile_id is a per-Profile Channel.
-- Cascade-deletes when the owning Library or (per-Profile) Profile is removed.
CREATE TABLE IF NOT EXISTS channel (
  id              TEXT    NOT NULL PRIMARY KEY,
  library_id      TEXT    NOT NULL REFERENCES library(id) ON DELETE CASCADE,
  profile_id      TEXT             REFERENCES profile(id) ON DELETE CASCADE,
  name            TEXT    NOT NULL,
  source_expr     TEXT    NOT NULL DEFAULT '{}',
  ordering_policy TEXT    NOT NULL DEFAULT 'shuffle',
  repeat_policy   TEXT    NOT NULL DEFAULT 'off',
  created_at_ms   INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS channel_library_profile_idx
  ON channel(library_id, profile_id);
