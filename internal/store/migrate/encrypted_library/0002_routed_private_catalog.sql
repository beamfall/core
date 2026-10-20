-- ENC-DB-3: private catalog rows routed to one Encrypted-Library DB.
--
-- These schema variants deliberately do not reference the global library table:
-- SQLite cannot enforce cross-DB foreign keys without ATTACH, and production
-- persistent ATTACH is rejected. The store validates the Library in catalog.db
-- before choosing this DB's writer.

CREATE TABLE IF NOT EXISTS media (
  id TEXT PRIMARY KEY,
  library_id TEXT NOT NULL,
  title TEXT NOT NULL,
  duration_seconds INTEGER NOT NULL,
  container TEXT NOT NULL,
  codec TEXT NOT NULL,
  trashed_at_ms INTEGER,
  media_class TEXT NOT NULL DEFAULT 'video',
  summary TEXT,
  year INTEGER,
  title_identified INTEGER NOT NULL DEFAULT 0,
  summary_identified INTEGER NOT NULL DEFAULT 0,
  year_identified INTEGER NOT NULL DEFAULT 0,
  video_width INTEGER NOT NULL DEFAULT 0,
  video_height INTEGER NOT NULL DEFAULT 0,
  mutated_by TEXT,
  extra INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_media_trashed ON media (trashed_at_ms);
CREATE INDEX IF NOT EXISTS media_extra_idx ON media(library_id) WHERE extra = 1;

CREATE VIRTUAL TABLE IF NOT EXISTS media_fts USING fts5(
  title,
  content='media',
  content_rowid='rowid'
);
CREATE TRIGGER IF NOT EXISTS media_fts_insert AFTER INSERT ON media BEGIN
  INSERT INTO media_fts(rowid, title) VALUES (new.rowid, new.title);
END;
CREATE TRIGGER IF NOT EXISTS media_fts_delete AFTER DELETE ON media BEGIN
  INSERT INTO media_fts(media_fts, rowid, title) VALUES ('delete', old.rowid, old.title);
END;
CREATE TRIGGER IF NOT EXISTS media_fts_update AFTER UPDATE OF title ON media BEGIN
  INSERT INTO media_fts(media_fts, rowid, title) VALUES ('delete', old.rowid, old.title);
  INSERT INTO media_fts(rowid, title) VALUES (new.rowid, new.title);
END;

CREATE TABLE IF NOT EXISTS media_file (
  id TEXT NOT NULL PRIMARY KEY,
  content_fingerprint TEXT NOT NULL UNIQUE,
  media_id TEXT NOT NULL REFERENCES media(id) ON DELETE CASCADE,
  path TEXT NOT NULL,
  version_label TEXT NOT NULL DEFAULT '',
  part_ordinal INTEGER NOT NULL DEFAULT 0,
  missing_at_ms INTEGER,
  last_seen_at_ms INTEGER NOT NULL DEFAULT (unixepoch() * 1000),
  size_bytes INTEGER NOT NULL DEFAULT 0,
  mtime_ms INTEGER NOT NULL DEFAULT 0,
  oshash TEXT NOT NULL DEFAULT '',
  phash TEXT NOT NULL DEFAULT '',
  provisional INTEGER NOT NULL DEFAULT 0,
  match_pinned INTEGER NOT NULL DEFAULT 0,
  decode_broken INTEGER NOT NULL DEFAULT 0,
  decode_checked_at_ms INTEGER
);
CREATE INDEX IF NOT EXISTS media_file_media_id_idx ON media_file(media_id);
CREATE UNIQUE INDEX IF NOT EXISTS media_file_available_slot_uq
  ON media_file(media_id, version_label, part_ordinal)
  WHERE missing_at_ms IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS media_file_available_path_uq
  ON media_file(path)
  WHERE missing_at_ms IS NULL;
CREATE INDEX IF NOT EXISTS media_file_oshash_idx
  ON media_file(oshash)
  WHERE missing_at_ms IS NULL;

CREATE TABLE IF NOT EXISTS media_file_stream (
  content_fingerprint TEXT NOT NULL REFERENCES media_file(content_fingerprint) ON DELETE CASCADE,
  stream_index INTEGER NOT NULL,
  type TEXT NOT NULL,
  codec TEXT NOT NULL,
  language TEXT NOT NULL DEFAULT '',
  title TEXT NOT NULL DEFAULT '',
  channels INTEGER NOT NULL DEFAULT 0,
  width INTEGER NOT NULL DEFAULT 0,
  height INTEGER NOT NULL DEFAULT 0,
  hdr TEXT NOT NULL DEFAULT '',
  dolby_vision INTEGER NOT NULL DEFAULT 0,
  level INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (content_fingerprint, stream_index)
);

CREATE TABLE IF NOT EXISTS media_file_font_attachment (
  content_fingerprint TEXT NOT NULL REFERENCES media_file(content_fingerprint) ON DELETE CASCADE,
  stream_index INTEGER NOT NULL,
  filename TEXT NOT NULL,
  mime_type TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (content_fingerprint, stream_index, filename)
);

CREATE TABLE IF NOT EXISTS media_file_sidecar_caption (
  content_fingerprint TEXT NOT NULL REFERENCES media_file(content_fingerprint) ON DELETE CASCADE,
  rel_path TEXT NOT NULL,
  format TEXT NOT NULL,
  language TEXT NOT NULL DEFAULT '',
  is_forced INTEGER NOT NULL DEFAULT 0,
  is_sdh INTEGER NOT NULL DEFAULT 0,
  kind TEXT NOT NULL DEFAULT 'caption',
  PRIMARY KEY (content_fingerprint, rel_path)
);

CREATE TABLE IF NOT EXISTS entity (
  id TEXT PRIMARY KEY,
  library_id TEXT NOT NULL,
  kind TEXT NOT NULL,
  title TEXT NOT NULL,
  trashed_at_ms INTEGER
);
CREATE INDEX IF NOT EXISTS entity_library_kind_idx ON entity(library_id, kind);
CREATE INDEX IF NOT EXISTS idx_entity_trashed ON entity (trashed_at_ms);

CREATE TABLE IF NOT EXISTS entity_field_int (
  entity_id TEXT NOT NULL REFERENCES entity(id) ON DELETE CASCADE,
  kind TEXT NOT NULL,
  field TEXT NOT NULL,
  field_type TEXT NOT NULL,
  value INTEGER NOT NULL,
  PRIMARY KEY(entity_id, field)
);
CREATE INDEX IF NOT EXISTS entity_field_int_kfv_idx ON entity_field_int(kind, field, value);

CREATE TABLE IF NOT EXISTS entity_field_real (
  entity_id TEXT NOT NULL REFERENCES entity(id) ON DELETE CASCADE,
  kind TEXT NOT NULL,
  field TEXT NOT NULL,
  field_type TEXT NOT NULL,
  value REAL NOT NULL,
  PRIMARY KEY(entity_id, field)
);
CREATE INDEX IF NOT EXISTS entity_field_real_kfv_idx ON entity_field_real(kind, field, value);

CREATE TABLE IF NOT EXISTS entity_field_text (
  entity_id TEXT NOT NULL REFERENCES entity(id) ON DELETE CASCADE,
  kind TEXT NOT NULL,
  field TEXT NOT NULL,
  field_type TEXT NOT NULL,
  value TEXT NOT NULL,
  PRIMARY KEY(entity_id, field)
);
CREATE INDEX IF NOT EXISTS entity_field_text_kfv_idx ON entity_field_text(kind, field, value);

CREATE TABLE IF NOT EXISTS entity_field_date (
  entity_id TEXT NOT NULL REFERENCES entity(id) ON DELETE CASCADE,
  kind TEXT NOT NULL,
  field TEXT NOT NULL,
  field_type TEXT NOT NULL,
  value_ms INTEGER NOT NULL,
  PRIMARY KEY(entity_id, field)
);
CREATE INDEX IF NOT EXISTS entity_field_date_kfv_idx ON entity_field_date(kind, field, value_ms);

CREATE TABLE IF NOT EXISTS entity_relation (
  from_entity_id TEXT NOT NULL REFERENCES entity(id) ON DELETE CASCADE,
  relation TEXT NOT NULL,
  to_entity_id TEXT REFERENCES entity(id) ON DELETE CASCADE,
  to_media_id TEXT REFERENCES media(id) ON DELETE CASCADE,
  attributes TEXT,
  sort_order INTEGER,
  CHECK ((to_entity_id IS NULL) <> (to_media_id IS NULL)),
  PRIMARY KEY(from_entity_id, relation, to_entity_id, to_media_id)
);

CREATE TABLE IF NOT EXISTS media_relation (
  from_media_id TEXT NOT NULL REFERENCES media(id) ON DELETE CASCADE,
  relation TEXT NOT NULL,
  to_media_id TEXT NOT NULL REFERENCES media(id) ON DELETE CASCADE,
  PRIMARY KEY(from_media_id, relation, to_media_id)
);
CREATE INDEX IF NOT EXISTS media_relation_to_idx ON media_relation(to_media_id, relation);

CREATE VIRTUAL TABLE IF NOT EXISTS entity_fts USING fts5(
  title,
  fields,
  entity_id UNINDEXED
);
CREATE TRIGGER IF NOT EXISTS entity_fts_delete AFTER DELETE ON entity BEGIN
  DELETE FROM entity_fts WHERE entity_id = old.id;
END;
