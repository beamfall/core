-- Searchable plugin-declared Media text fields use an external-content FTS5
-- index. The typed side table remains authoritative; triggers keep the index
-- current and the INSERT backfills databases upgraded with existing values.
CREATE VIRTUAL TABLE media_field_fts USING fts5(
  value,
  content='media_field_text',
  content_rowid='rowid'
);

CREATE TRIGGER media_field_text_ai AFTER INSERT ON media_field_text BEGIN
  INSERT INTO media_field_fts(rowid, value) VALUES (new.rowid, new.value);
END;

CREATE TRIGGER media_field_text_ad AFTER DELETE ON media_field_text BEGIN
  INSERT INTO media_field_fts(media_field_fts, rowid, value)
    VALUES ('delete', old.rowid, old.value);
END;

CREATE TRIGGER media_field_text_au AFTER UPDATE OF value ON media_field_text BEGIN
  INSERT INTO media_field_fts(media_field_fts, rowid, value)
    VALUES ('delete', old.rowid, old.value);
  INSERT INTO media_field_fts(rowid, value) VALUES (new.rowid, new.value);
END;

INSERT INTO media_field_fts(rowid, value)
  SELECT rowid, value FROM media_field_text;

-- Tombstones are typed independently from the AI proposal queue. An empty
-- field_type preserves every legacy rejected proposal as a wildcard during
-- upgrade, matching the pre-migration rejection behavior.
CREATE TABLE media_field_rejection (
  media_id      TEXT NOT NULL REFERENCES media(id) ON DELETE CASCADE,
  field         TEXT NOT NULL,
  field_type    TEXT NOT NULL,
  value         TEXT NOT NULL,
  rejected_at_ms INTEGER NOT NULL,
  PRIMARY KEY (media_id, field, field_type, value)
);

INSERT INTO media_field_rejection(media_id, field, field_type, value, rejected_at_ms)
  SELECT media_id, tag_kind, '', value, MAX(proposed_at_ms)
  FROM pending_ai_tag
  WHERE status = 'rejected'
  GROUP BY media_id, tag_kind, value;
