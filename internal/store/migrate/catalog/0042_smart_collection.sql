-- SmartCollections (ADR-0011 §10): Library-level Collections whose membership
-- is a persisted Source expression evaluated live on each request. source_expr
-- is an opaque client expression (same vocabulary as the filter-bar source);
-- the server stores and returns it without interpretation. Cascade-deletes with
-- the Library.
CREATE TABLE IF NOT EXISTS smart_collection (
  id             TEXT    NOT NULL PRIMARY KEY,
  library_id     TEXT    NOT NULL REFERENCES library(id) ON DELETE CASCADE,
  name           TEXT    NOT NULL,
  source_expr    TEXT    NOT NULL DEFAULT '{}',
  created_at_ms  INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS smart_collection_library_idx
  ON smart_collection(library_id);
