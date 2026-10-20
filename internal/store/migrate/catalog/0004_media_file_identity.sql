CREATE TABLE IF NOT EXISTS media_file_new (
  content_fingerprint TEXT PRIMARY KEY,
  media_id TEXT NOT NULL REFERENCES media(id) ON DELETE CASCADE,
  path TEXT NOT NULL,
  missing_at INTEGER,
  last_seen_at INTEGER NOT NULL DEFAULT (unixepoch())
);

INSERT INTO media_file_new(content_fingerprint, media_id, path, missing_at, last_seen_at)
SELECT content_fingerprint, media_id, path, NULL, unixepoch()
FROM media_file;

DROP TABLE media_file;
ALTER TABLE media_file_new RENAME TO media_file;

CREATE INDEX media_file_media_id_idx ON media_file(media_id);
CREATE UNIQUE INDEX media_file_available_media_uq
  ON media_file(media_id)
  WHERE missing_at IS NULL;
CREATE UNIQUE INDEX media_file_available_path_uq
  ON media_file(path)
  WHERE missing_at IS NULL;
