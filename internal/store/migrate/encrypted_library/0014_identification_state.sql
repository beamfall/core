-- CORE-0706 (COREAI-PRIVATE-INGEST-PLANE-1, slice 1): per-Media
-- identification state lives on the Media's own catalog plane. External IDs,
-- scraped artwork candidates (whose source URLs name the target) and the
-- Unmatched queue row for an Encrypted Library's Media are written here, in
-- the sealed metadata DB, never in the shared catalog. Shapes mirror catalog
-- 0026, 0029 and 0056 so one SQL text serves both planes; every row cascades
-- with its private Media.
CREATE TABLE media_external_id (
  media_id TEXT NOT NULL REFERENCES media(id) ON DELETE CASCADE,
  scheme TEXT NOT NULL,
  value TEXT NOT NULL,
  PRIMARY KEY (media_id, scheme, value)
);
CREATE INDEX media_external_id_lookup_idx ON media_external_id(scheme, value);

CREATE TABLE artwork_candidate (
  id TEXT PRIMARY KEY,
  media_id TEXT NOT NULL REFERENCES media(id) ON DELETE CASCADE,
  slot TEXT NOT NULL,
  source_url TEXT NOT NULL,
  plugin_id TEXT NOT NULL DEFAULT '',
  scraper_id TEXT NOT NULL DEFAULT '',
  applied INTEGER NOT NULL DEFAULT 0,
  created_at_ms INTEGER NOT NULL,
  UNIQUE(media_id, slot, source_url)
);
CREATE INDEX artwork_candidate_media_slot_idx ON artwork_candidate(media_id, slot);

CREATE TABLE unmatched_queue (
  media_id TEXT NOT NULL PRIMARY KEY REFERENCES media(id) ON DELETE CASCADE,
  plugin_id TEXT NOT NULL,
  scraper_id TEXT NOT NULL,
  confidence REAL NOT NULL,
  threshold REAL NOT NULL,
  candidate_id TEXT NOT NULL DEFAULT '',
  candidate_title TEXT NOT NULL DEFAULT '',
  queued_at_ms INTEGER NOT NULL
);
CREATE INDEX unmatched_queue_queued_idx ON unmatched_queue(queued_at_ms);
