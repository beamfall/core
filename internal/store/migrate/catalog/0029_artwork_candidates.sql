CREATE TABLE IF NOT EXISTS artwork_candidate (
  id TEXT PRIMARY KEY,
  media_id TEXT NOT NULL,
  slot TEXT NOT NULL,
  source_url TEXT NOT NULL,
  plugin_id TEXT NOT NULL DEFAULT '',
  scraper_id TEXT NOT NULL DEFAULT '',
  applied INTEGER NOT NULL DEFAULT 0,
  created_at_ms INTEGER NOT NULL,
  UNIQUE(media_id, slot, source_url)
);

CREATE INDEX IF NOT EXISTS artwork_candidate_media_slot_idx ON artwork_candidate(media_id, slot);
