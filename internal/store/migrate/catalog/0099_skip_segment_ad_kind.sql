-- PODAD-1 (ADR-0193 Decision 2/5, amending ADR-0048 Decision 1): the closed
-- skip-segment vocabulary gains a fourth kind, `ad`, plus an INTERNAL detection
-- provenance `flavor` that is never serialized to a client.
--
-- Three schema moves, all additive-by-widening — no row is dropped:
--
--   1. marker.segment_kind CHECK grows 'ad'; marker gains a `flavor` column.
--   2. marker_suppression.segment_kind CHECK grows 'ad', so delete-and-suppress
--      (ADR-0048 D5) works for the new kind instead of failing at the schema.
--   3. a new segment_detection table: the never-served internal record of what
--      the detector saw, including the sub-threshold `host-read` ads ADR-0193 D5
--      requires be "detected internally, flavor + confidence recorded for eval,
--      but no marker served".
--
-- flavor's marker CHECK deliberately omits 'host-read'. ADR-0193 D5: a surfaced
-- `ad` marker is one the client WILL auto-skip (auto-skip keys on segmentKind
-- alone; MarkerInfo carries no per-marker gate), so a host-read row in `marker`
-- IS a surfaced host-read ad. It can only live in segment_detection. The two
-- cross-column CHECKs make the remaining flavor invariants schema-enforced
-- rather than application-only: a flavor implies a detected `ad`, and a detected
-- `ad` implies a flavor. IFNULL is load-bearing — segment_kind is nullable and
-- SQLite treats a NULL CHECK result as a PASS, so a bare comparison would let a
-- flavored kindless row through.
--
-- SQLite cannot ALTER a CHECK, so both constrained tables are recreated with the
-- standard 12-step (as catalog 0068 did). Unlike 0068 this reproduces the
-- CURRENT marker shape: 0017 base + 0051 created_by_profile_id + 0063
-- provenance/method/confidence + 0079 trashed_at_ms. Dropping trashed_at_ms
-- would break markerColumns/scanMarkerRow and destroy every soft-deleted Marker.
-- marker_created_by_idx (0051) is deliberately NOT recreated: 0068's DROP TABLE
-- already removed it and it has not existed since; resurrecting it here would be
-- an unrequested schema change.
--
-- marker_tag (0017) and marker_core_tag (0076) reference marker ON DELETE
-- CASCADE and the writer opens with foreign_keys(1), so DROP TABLE marker can
-- empty them — and PRAGMA foreign_key_check would NOT notice, because a cascade
-- leaves no dangling reference. Their rows are therefore snapshotted first and
-- restored with INSERT OR IGNORE, which is correct whether or not the cascade
-- actually fires.

CREATE TABLE marker_tag_rebuild_backup AS SELECT * FROM marker_tag;
CREATE TABLE marker_core_tag_rebuild_backup AS SELECT * FROM marker_core_tag;

CREATE TABLE marker_new (
  id TEXT PRIMARY KEY,
  media_id TEXT NOT NULL REFERENCES media(id) ON DELETE CASCADE,
  title TEXT NOT NULL DEFAULT '',
  start_seconds REAL NOT NULL CHECK (start_seconds >= 0),
  end_seconds REAL CHECK (end_seconds > start_seconds),
  segment_kind TEXT CHECK (segment_kind IN ('intro', 'recap', 'credits', 'ad')),
  created_at_ms INTEGER NOT NULL,
  updated_at_ms INTEGER NOT NULL,
  created_by_profile_id TEXT,
  provenance TEXT NOT NULL DEFAULT 'manual' CHECK (provenance IN ('manual', 'detected')),
  method TEXT NOT NULL DEFAULT '',
  confidence REAL NOT NULL DEFAULT 0,
  trashed_at_ms INTEGER,
  flavor TEXT NOT NULL DEFAULT '' CHECK (flavor IN ('', 'inserted')),
  CHECK (flavor = '' OR (IFNULL(segment_kind, '') = 'ad' AND provenance = 'detected')),
  CHECK (IFNULL(segment_kind, '') <> 'ad' OR provenance <> 'detected' OR flavor <> '')
);

INSERT INTO marker_new(id, media_id, title, start_seconds, end_seconds, segment_kind,
                       created_at_ms, updated_at_ms, created_by_profile_id,
                       provenance, method, confidence, trashed_at_ms)
  SELECT id, media_id, title, start_seconds, end_seconds, segment_kind,
         created_at_ms, updated_at_ms, created_by_profile_id,
         provenance, method, confidence, trashed_at_ms
  FROM marker;

DROP TABLE marker;
ALTER TABLE marker_new RENAME TO marker;
CREATE INDEX marker_media_start_idx ON marker(media_id, start_seconds);
CREATE INDEX idx_marker_trashed ON marker (media_id, trashed_at_ms)
  WHERE trashed_at_ms IS NOT NULL;

INSERT OR IGNORE INTO marker_tag SELECT * FROM marker_tag_rebuild_backup;
INSERT OR IGNORE INTO marker_core_tag SELECT * FROM marker_core_tag_rebuild_backup;
DROP TABLE marker_tag_rebuild_backup;
DROP TABLE marker_core_tag_rebuild_backup;

-- marker_suppression (0063) has no children, so its rebuild needs no snapshot.
CREATE TABLE marker_suppression_new (
  media_id      TEXT NOT NULL REFERENCES media(id) ON DELETE CASCADE,
  segment_kind  TEXT NOT NULL CHECK (segment_kind IN ('intro', 'recap', 'credits', 'ad')),
  created_at_ms INTEGER NOT NULL,
  PRIMARY KEY (media_id, segment_kind)
);

INSERT INTO marker_suppression_new(media_id, segment_kind, created_at_ms)
  SELECT media_id, segment_kind, created_at_ms FROM marker_suppression;

DROP TABLE marker_suppression;
ALTER TABLE marker_suppression_new RENAME TO marker_suppression;

-- The internal detection/eval record (ADR-0193 D5). Never served: no graph
-- field, no wire shape, no dispatch entry reads it. It is where a dropped
-- host-read ad's flavor + confidence survives without becoming a marker.
CREATE TABLE segment_detection (
  id            TEXT PRIMARY KEY,
  media_id      TEXT NOT NULL REFERENCES media(id) ON DELETE CASCADE,
  segment_kind  TEXT NOT NULL CHECK (segment_kind IN ('intro', 'recap', 'credits', 'ad')),
  start_seconds REAL NOT NULL CHECK (start_seconds >= 0),
  end_seconds   REAL NOT NULL CHECK (end_seconds > start_seconds),
  method        TEXT NOT NULL,
  flavor        TEXT NOT NULL DEFAULT '' CHECK (flavor IN ('', 'inserted', 'host-read')),
  confidence    REAL NOT NULL CHECK (confidence >= 0 AND confidence <= 1),
  created_at_ms INTEGER NOT NULL,
  CHECK (flavor = '' OR segment_kind = 'ad'),
  CHECK (segment_kind <> 'ad' OR flavor <> '')
);
CREATE INDEX segment_detection_media_idx ON segment_detection(media_id, start_seconds);
