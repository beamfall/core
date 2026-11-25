-- CORE-0768: static Collections of an Encrypted Library live in its sealed
-- catalog plane (ADR-0045). The shape mirrors catalog 0078; library_id carries
-- no foreign key because the library table stays in catalog.db, and members
-- reference this plane's media table so a visible encrypted Media can join.
CREATE TABLE collection (
  id             TEXT    NOT NULL PRIMARY KEY,
  library_id     TEXT    NOT NULL,
  parent_id      TEXT    REFERENCES collection(id) ON DELETE SET NULL,
  name           TEXT    NOT NULL,
  sort_order     INTEGER NOT NULL DEFAULT 0,
  trashed_at_ms  INTEGER,
  created_at_ms  INTEGER NOT NULL,
  updated_at_ms  INTEGER NOT NULL,
  CHECK (parent_id IS NULL OR parent_id <> id)
);

CREATE INDEX collection_library_parent_idx
  ON collection(library_id, parent_id, trashed_at_ms, sort_order, name);

CREATE TABLE collection_media (
  collection_id  TEXT    NOT NULL REFERENCES collection(id) ON DELETE CASCADE,
  media_id       TEXT    NOT NULL REFERENCES media(id) ON DELETE CASCADE,
  sort_order     INTEGER NOT NULL DEFAULT 0,
  added_at_ms    INTEGER NOT NULL,
  PRIMARY KEY (collection_id, media_id)
);

CREATE INDEX collection_media_collection_order_idx
  ON collection_media(collection_id, sort_order, media_id);

CREATE INDEX collection_media_media_idx
  ON collection_media(media_id);
