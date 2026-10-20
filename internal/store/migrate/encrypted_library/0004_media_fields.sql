-- PHOTOPAR-6: plugin-declared Media fields belong to the protected catalog
-- plane, including their precedence and manual-rejection state.

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

CREATE TABLE media_field_provenance (
  media_id TEXT NOT NULL REFERENCES media(id) ON DELETE CASCADE,
  field TEXT NOT NULL,
  source TEXT NOT NULL,
  PRIMARY KEY (media_id, field)
);

INSERT INTO media_field_provenance(media_id, field, source)
  SELECT id, 'title', 'operator' FROM media WHERE title_identified = 1;
INSERT INTO media_field_provenance(media_id, field, source)
  SELECT id, 'summary', 'operator' FROM media WHERE summary_identified = 1;
INSERT INTO media_field_provenance(media_id, field, source)
  SELECT id, 'year', 'operator' FROM media WHERE year_identified = 1;

CREATE TABLE media_field_rejection (
  media_id TEXT NOT NULL REFERENCES media(id) ON DELETE CASCADE,
  field TEXT NOT NULL,
  field_type TEXT NOT NULL,
  value TEXT NOT NULL,
  rejected_at_ms INTEGER NOT NULL,
  PRIMARY KEY (media_id, field, field_type, value)
);

CREATE VIRTUAL TABLE media_field_fts USING fts5(
  value,
  content='media_field_text',
  content_rowid='rowid'
);
CREATE TRIGGER media_field_text_ai AFTER INSERT ON media_field_text BEGIN
  INSERT INTO media_field_fts(rowid, value) VALUES (new.rowid, new.value);
END;
CREATE TRIGGER media_field_text_ad AFTER DELETE ON media_field_text BEGIN
  INSERT INTO media_field_fts(media_field_fts, rowid, value) VALUES ('delete', old.rowid, old.value);
END;
CREATE TRIGGER media_field_text_au AFTER UPDATE OF value ON media_field_text BEGIN
  INSERT INTO media_field_fts(media_field_fts, rowid, value) VALUES ('delete', old.rowid, old.value);
  INSERT INTO media_field_fts(rowid, value) VALUES (new.rowid, new.value);
END;
INSERT INTO media_field_fts(rowid, value)
  SELECT rowid, value FROM media_field_text;
