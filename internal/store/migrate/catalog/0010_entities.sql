CREATE TABLE entity (
  id TEXT PRIMARY KEY,
  library_id TEXT NOT NULL REFERENCES library(id),
  kind TEXT NOT NULL,
  title TEXT NOT NULL
);

CREATE INDEX entity_library_kind_idx ON entity(library_id, kind);

-- Typed Entity field side tables (ADR-0011 §2): one row per declared field
-- value, keyed by field name, with the composite (kind, field, value) index
-- each table carries for filter/sort queries.
CREATE TABLE entity_field_int (
  entity_id TEXT NOT NULL REFERENCES entity(id) ON DELETE CASCADE,
  kind TEXT NOT NULL,
  field TEXT NOT NULL,
  field_type TEXT NOT NULL,
  value INTEGER NOT NULL,
  PRIMARY KEY (entity_id, field)
);
CREATE INDEX entity_field_int_kfv_idx ON entity_field_int(kind, field, value);

CREATE TABLE entity_field_real (
  entity_id TEXT NOT NULL REFERENCES entity(id) ON DELETE CASCADE,
  kind TEXT NOT NULL,
  field TEXT NOT NULL,
  field_type TEXT NOT NULL,
  value REAL NOT NULL,
  PRIMARY KEY (entity_id, field)
);
CREATE INDEX entity_field_real_kfv_idx ON entity_field_real(kind, field, value);

CREATE TABLE entity_field_text (
  entity_id TEXT NOT NULL REFERENCES entity(id) ON DELETE CASCADE,
  kind TEXT NOT NULL,
  field TEXT NOT NULL,
  field_type TEXT NOT NULL,
  value TEXT NOT NULL,
  PRIMARY KEY (entity_id, field)
);
CREATE INDEX entity_field_text_kfv_idx ON entity_field_text(kind, field, value);

CREATE TABLE entity_field_date (
  entity_id TEXT NOT NULL REFERENCES entity(id) ON DELETE CASCADE,
  kind TEXT NOT NULL,
  field TEXT NOT NULL,
  field_type TEXT NOT NULL,
  value_ms INTEGER NOT NULL,
  PRIMARY KEY (entity_id, field)
);
CREATE INDEX entity_field_date_kfv_idx ON entity_field_date(kind, field, value_ms);

-- Relations in one table with typed endpoints, indexed both directions;
-- attributes are JSON — rendered, never filtered on in v1 (ADR-0011 §2).
CREATE TABLE entity_relation (
  from_entity_id TEXT NOT NULL REFERENCES entity(id) ON DELETE CASCADE,
  relation TEXT NOT NULL,
  to_entity_id TEXT REFERENCES entity(id) ON DELETE CASCADE,
  to_media_id TEXT REFERENCES media(id) ON DELETE CASCADE,
  attributes TEXT,
  CHECK ((to_entity_id IS NULL) <> (to_media_id IS NULL))
);
CREATE UNIQUE INDEX entity_relation_entity_uq
  ON entity_relation(from_entity_id, relation, to_entity_id)
  WHERE to_entity_id IS NOT NULL;
CREATE UNIQUE INDEX entity_relation_media_uq
  ON entity_relation(from_entity_id, relation, to_media_id)
  WHERE to_media_id IS NOT NULL;
CREATE INDEX entity_relation_to_entity_idx
  ON entity_relation(to_entity_id, relation)
  WHERE to_entity_id IS NOT NULL;
CREATE INDEX entity_relation_to_media_idx
  ON entity_relation(to_media_id, relation)
  WHERE to_media_id IS NOT NULL;
