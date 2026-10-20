-- 1–N Media Files per Media: Versions + Parts (ADR-0009 §2). Until now
-- media_file's identity was content_fingerprint as PRIMARY KEY plus a
-- media_id UNIQUE-while-available index (0004/0005), which forced exactly ONE
-- available file per Media. ADR-0009 §2 needs many files per Media:
--   * Versions — alternate encodes of the same content (a 4K HDR rip and a
--     1080p SDR rip of one film); playback picks the best Version for the
--     client before transcode.
--   * Parts    — ordered files that form one continuous timeline (a film
--     split across two discs); they play back-to-back as a single timeline.
--
-- This is the hardest-to-retrofit layer, so the table rebuild is done once,
-- carefully, here. APPEND-ONLY: 0003 (and the 0004/0005 rebuilds) are already
-- applied on beta databases — editing them would strand those DBs in a schema
-- no migration reconciles. This is a NEW forward migration.
--
-- SQLite cannot drop/alter a PRIMARY KEY in place, so this uses the standard
-- table-rebuild (CREATE _new → INSERT…SELECT → DROP → RENAME, the same shape
-- as catalog 0004/0005). Every existing row is preserved (invariant #6 —
-- nothing is silently dropped): each migrates to the DEFAULT Version
-- (version_label = '') and the SINGLE Part (part_ordinal = 0), which is exactly
-- the pre-migration single-file semantics, so existing single-file Media keep
-- playing unchanged.
--
-- content_fingerprint stays the file-plane content identity (ADR-0009) and is
-- carried into the new id PRIMARY KEY for every migrated row (it was already
-- unique — it was the old PK — so this is collision-free and deterministic).
-- It is also kept as its own column with a UNIQUE constraint, because
-- media_file_stream / media_file_font_attachment (0050) and media_embedding
-- (0045) hang their rows off media_file by content_fingerprint; the FK targets
-- recorded in those child tables resolve by table name + referenced column, so
-- content_fingerprint MUST remain UNIQUE in the rebuilt table or
-- PRAGMA foreign_key_check (run after every migration) would fail.

CREATE TABLE media_file_vp (
  -- id is media_file's OWN identity (PK), independent of the Media it backs.
  -- Seeded from content_fingerprint for migrated rows (unique, stable); the
  -- write path sets it to content_fingerprint for new rows too, so a re-scan of
  -- the same bytes upserts the same logical file.
  id TEXT NOT NULL PRIMARY KEY,
  content_fingerprint TEXT NOT NULL,
  -- media_id is now a 1:N FK: a Media may have many files (Versions x Parts).
  media_id TEXT NOT NULL REFERENCES media(id) ON DELETE CASCADE,
  path TEXT NOT NULL,
  -- version_label names the Version (alternate encode) this file belongs to.
  -- '' is the DEFAULT Version: every pre-migration row, and every single-file
  -- Media, lives here. Files sharing a (media_id, version_label) are the same
  -- Version; distinct labels are distinct Versions to negotiate between.
  version_label TEXT NOT NULL DEFAULT '',
  -- part_ordinal orders the Parts within one Version into a single timeline
  -- (0, 1, 2, …). 0 is the sole Part of a single-file Version — the
  -- pre-migration semantics. Files in one Version play in ascending ordinal.
  part_ordinal INTEGER NOT NULL DEFAULT 0,
  missing_at_ms INTEGER,
  last_seen_at_ms INTEGER NOT NULL DEFAULT (unixepoch() * 1000),
  size_bytes INTEGER NOT NULL DEFAULT 0,
  mtime_ms INTEGER NOT NULL DEFAULT 0,
  oshash TEXT NOT NULL DEFAULT '',
  phash TEXT NOT NULL DEFAULT '',
  provisional INTEGER NOT NULL DEFAULT 0,
  UNIQUE (content_fingerprint)
);

INSERT INTO media_file_vp(
  id, content_fingerprint, media_id, path, version_label, part_ordinal,
  missing_at_ms, last_seen_at_ms, size_bytes, mtime_ms, oshash, phash, provisional)
SELECT
  content_fingerprint, content_fingerprint, media_id, path, '', 0,
  missing_at_ms, last_seen_at_ms, size_bytes, mtime_ms, oshash, phash, provisional
FROM media_file;

DROP TABLE media_file;
ALTER TABLE media_file_vp RENAME TO media_file;

-- media_id lookup index (every read fetches a Media's files by media_id).
CREATE INDEX media_file_media_id_idx ON media_file(media_id);

-- One available file per (Media, Version, Part) slot, and the ordering index in
-- one: listing a Media's available Versions+Parts walks this index in
-- (version_label, part_ordinal) order. Replaces the old
-- media_file_available_media_uq, whose single-row-per-media uniqueness is the
-- constraint this migration deliberately relaxes — 1:N is now allowed; two
-- distinct files just must not claim the same Version+Part slot.
CREATE UNIQUE INDEX media_file_available_slot_uq
  ON media_file(media_id, version_label, part_ordinal)
  WHERE missing_at_ms IS NULL;

-- One available file per path (unchanged from 0004/0005): a path backs at most
-- one live file.
CREATE UNIQUE INDEX media_file_available_path_uq
  ON media_file(path)
  WHERE missing_at_ms IS NULL;

-- Duplicate-candidate self-join index (ADR-0042 Library Health), recreated
-- byte-for-byte from 0048 — the rebuild dropped it with the old table.
CREATE INDEX IF NOT EXISTS media_file_oshash_idx
ON media_file(oshash)
WHERE missing_at_ms IS NULL;
