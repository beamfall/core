CREATE VIRTUAL TABLE IF NOT EXISTS media_fts USING fts5(
  title,
  content='media',
  content_rowid='rowid'
);

INSERT INTO media_fts(rowid, title)
  SELECT rowid, title FROM media;

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
