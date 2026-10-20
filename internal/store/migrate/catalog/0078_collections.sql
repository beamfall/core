-- Static ordered Collections (WEB-5F / ADR-0001 / ADR-0061): user-curated,
-- nameable groups of Media with explicit order. SmartCollections remain in
-- smart_collection; this table is the manual Collection/Sub-collection spine.
CREATE TABLE IF NOT EXISTS collection (
  id             TEXT    NOT NULL PRIMARY KEY,
  library_id     TEXT    NOT NULL REFERENCES library(id) ON DELETE CASCADE,
  parent_id      TEXT    REFERENCES collection(id) ON DELETE SET NULL,
  name           TEXT    NOT NULL,
  sort_order     INTEGER NOT NULL DEFAULT 0,
  trashed_at_ms  INTEGER,
  created_at_ms  INTEGER NOT NULL,
  updated_at_ms  INTEGER NOT NULL,
  CHECK (parent_id IS NULL OR parent_id <> id)
);

CREATE INDEX IF NOT EXISTS collection_library_parent_idx
  ON collection(library_id, parent_id, trashed_at_ms, sort_order, name);

CREATE TABLE IF NOT EXISTS collection_media (
  collection_id  TEXT    NOT NULL REFERENCES collection(id) ON DELETE CASCADE,
  media_id       TEXT    NOT NULL REFERENCES media(id) ON DELETE CASCADE,
  sort_order     INTEGER NOT NULL DEFAULT 0,
  added_at_ms    INTEGER NOT NULL,
  PRIMARY KEY (collection_id, media_id)
);

CREATE INDEX IF NOT EXISTS collection_media_collection_order_idx
  ON collection_media(collection_id, sort_order, media_id);

CREATE INDEX IF NOT EXISTS collection_media_media_idx
  ON collection_media(media_id);

-- Smart Collection source editor substrate: keep the match expression and sort
-- expression separate so the editor can round-trip sort without parsing the
-- Source expression JSON.
ALTER TABLE smart_collection ADD COLUMN sort_expr TEXT NOT NULL DEFAULT '{}';
