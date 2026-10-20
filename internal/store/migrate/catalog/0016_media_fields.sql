-- Typed Media field side tables: plugin-declared fields on the wire entity
-- Media itself (the Bundle.MediaFields contract), mirroring the Entity field
-- side tables from 0010. One row per declared field value, keyed by field
-- name; no kind column — Media has no kind, so each table carries a
-- (field, value) index for filter/sort queries instead.
CREATE TABLE media_field_int (
  media_id TEXT NOT NULL REFERENCES media(id) ON DELETE CASCADE,
  field TEXT NOT NULL,
  field_type TEXT NOT NULL,
  value INTEGER NOT NULL,
  PRIMARY KEY (media_id, field)
);
CREATE INDEX media_field_int_fv_idx ON media_field_int(field, value);

CREATE TABLE media_field_real (
  media_id TEXT NOT NULL REFERENCES media(id) ON DELETE CASCADE,
  field TEXT NOT NULL,
  field_type TEXT NOT NULL,
  value REAL NOT NULL,
  PRIMARY KEY (media_id, field)
);
CREATE INDEX media_field_real_fv_idx ON media_field_real(field, value);

CREATE TABLE media_field_text (
  media_id TEXT NOT NULL REFERENCES media(id) ON DELETE CASCADE,
  field TEXT NOT NULL,
  field_type TEXT NOT NULL,
  value TEXT NOT NULL,
  PRIMARY KEY (media_id, field)
);
CREATE INDEX media_field_text_fv_idx ON media_field_text(field, value);

CREATE TABLE media_field_date (
  media_id TEXT NOT NULL REFERENCES media(id) ON DELETE CASCADE,
  field TEXT NOT NULL,
  field_type TEXT NOT NULL,
  value_ms INTEGER NOT NULL,
  PRIMARY KEY (media_id, field)
);
CREATE INDEX media_field_date_fv_idx ON media_field_date(field, value_ms);
