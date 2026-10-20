CREATE TABLE IF NOT EXISTS media_file (
  media_id TEXT PRIMARY KEY REFERENCES media(id) ON DELETE CASCADE,
  path TEXT NOT NULL,
  content_fingerprint TEXT NOT NULL
);
