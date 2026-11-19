-- CORE-2583: hidden media rowids can be reused after purge. Keep insertion
-- identity independently, without rebuilding media or changing its FTS rowids.
CREATE TABLE media_insert_seq (
  seq INTEGER PRIMARY KEY AUTOINCREMENT,
  media_id TEXT NOT NULL UNIQUE
);

INSERT INTO media_insert_seq(media_id) SELECT id FROM media ORDER BY rowid;

CREATE TRIGGER media_insert_seq_insert AFTER INSERT ON media BEGIN
  INSERT INTO media_insert_seq(media_id) VALUES (new.id);
END;

CREATE TRIGGER media_insert_seq_delete AFTER DELETE ON media BEGIN
  DELETE FROM media_insert_seq WHERE media_id = old.id;
END;
