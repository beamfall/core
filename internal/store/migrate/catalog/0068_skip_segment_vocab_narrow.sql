-- ENR-5 hard-narrow (ADR-0048 Decision 1; user decision 2026-06-16): the skip-
-- segment vocabulary is narrowed from five kinds to the three auto-skippable
-- shapes (intro / recap / credits). The ADR-0027 preview/commercial kinds were
-- DEFERRED by ADR-0048 and are now removed from the SCHEMA, not merely write-
-- blocked: the segment_kind CHECK is tightened to the three kinds so the DB
-- itself rejects them. SQLite cannot ALTER a CHECK in place, so the marker table
-- is recreated (the standard 12-step). No real production data exists yet (data
-- waiver), so any preview/commercial marker rows are dropped along with their
-- marker_tag children, keeping the post-migration PRAGMA foreign_key_check clean.
--
-- The recreated table reproduces marker as it stands after 0017 (base + media
-- CASCADE + the start-order index), 0051 (created_by_profile_id) and 0063
-- (provenance/method/confidence) EXACTLY, changing ONLY the segment_kind CHECK
-- list. Column order is preserved (0017 columns, then the 0051 column, then the
-- 0063 columns) so nothing that reads marker shifts.

-- Drop the tags of the rows that are about to disappear (FK child, ON DELETE
-- CASCADE — but we are not relying on cascade here since the recreation drops
-- the parent table; clean them explicitly so foreign_key_check passes).
DELETE FROM marker_tag WHERE marker_id IN (
  SELECT id FROM marker WHERE segment_kind IN ('preview', 'commercial')
);

CREATE TABLE marker_new (
  id TEXT PRIMARY KEY,
  media_id TEXT NOT NULL REFERENCES media(id) ON DELETE CASCADE,
  title TEXT NOT NULL DEFAULT '',
  start_seconds REAL NOT NULL CHECK (start_seconds >= 0),
  end_seconds REAL CHECK (end_seconds > start_seconds),
  segment_kind TEXT CHECK (segment_kind IN ('intro', 'recap', 'credits')),
  created_at_ms INTEGER NOT NULL,
  updated_at_ms INTEGER NOT NULL,
  created_by_profile_id TEXT,
  provenance TEXT NOT NULL DEFAULT 'manual' CHECK (provenance IN ('manual', 'detected')),
  method TEXT NOT NULL DEFAULT '',
  confidence REAL NOT NULL DEFAULT 0
);

INSERT INTO marker_new
  SELECT id, media_id, title, start_seconds, end_seconds, segment_kind,
         created_at_ms, updated_at_ms, created_by_profile_id, provenance, method, confidence
  FROM marker
  WHERE segment_kind IS NULL OR segment_kind IN ('intro', 'recap', 'credits');

DROP TABLE marker;
ALTER TABLE marker_new RENAME TO marker;
CREATE INDEX marker_media_start_idx ON marker(media_id, start_seconds);
