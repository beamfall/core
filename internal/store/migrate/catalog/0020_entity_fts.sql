-- Entity full-text search (ADR-0011 FTS posture, extended to Entities): one
-- standalone FTS5 table over the entity title plus the plugin-declared
-- Searchable text field values. Unlike media_fts (content-table triggers over
-- one column), the fields column is composed in Go on every UpsertEntity —
-- which field values are searchable is plugin data registered at activation,
-- unknowable to SQL triggers. Deletion stays a trigger: removing an entity row
-- (orphan GC) always removes its FTS row, whatever the declarations say.
CREATE VIRTUAL TABLE IF NOT EXISTS entity_fts USING fts5(
  title,
  fields,
  entity_id UNINDEXED
);

INSERT INTO entity_fts(entity_id, title, fields)
  SELECT id, title, '' FROM entity;

CREATE TRIGGER IF NOT EXISTS entity_fts_delete AFTER DELETE ON entity BEGIN
  DELETE FROM entity_fts WHERE entity_id = old.id;
END;
