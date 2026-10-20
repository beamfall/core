ALTER TABLE auth_session RENAME COLUMN expires_at TO expires_at_ms;
UPDATE auth_session SET expires_at_ms = expires_at_ms * 1000 WHERE expires_at_ms IS NOT NULL;

ALTER TABLE pairing_code RENAME COLUMN expires_at TO expires_at_ms;
ALTER TABLE pairing_code RENAME COLUMN consumed_at TO consumed_at_ms;
UPDATE pairing_code SET expires_at_ms = expires_at_ms * 1000;
UPDATE pairing_code SET consumed_at_ms = consumed_at_ms * 1000 WHERE consumed_at_ms IS NOT NULL;

CREATE TABLE media_file_ms (
  content_fingerprint TEXT PRIMARY KEY,
  media_id TEXT NOT NULL REFERENCES media(id) ON DELETE CASCADE,
  path TEXT NOT NULL,
  missing_at_ms INTEGER,
  last_seen_at_ms INTEGER NOT NULL DEFAULT (unixepoch() * 1000)
);

INSERT INTO media_file_ms(content_fingerprint, media_id, path, missing_at_ms, last_seen_at_ms)
SELECT content_fingerprint, media_id, path, missing_at * 1000, last_seen_at * 1000
FROM media_file;

DROP TABLE media_file;
ALTER TABLE media_file_ms RENAME TO media_file;

CREATE INDEX media_file_media_id_idx ON media_file(media_id);
CREATE UNIQUE INDEX media_file_available_media_uq
  ON media_file(media_id)
  WHERE missing_at_ms IS NULL;
CREATE UNIQUE INDEX media_file_available_path_uq
  ON media_file(path)
  WHERE missing_at_ms IS NULL;
