CREATE TABLE IF NOT EXISTS media_metadata_provenance (
  media_id TEXT PRIMARY KEY REFERENCES media(id) ON DELETE CASCADE,
  plugin_id TEXT NOT NULL,
  scraper_id TEXT NOT NULL,
  updated_at_ms INTEGER NOT NULL
);
