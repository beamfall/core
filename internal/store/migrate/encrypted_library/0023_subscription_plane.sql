-- CORE-3511 (CORE-0706): activate Encrypted Library Subscriptions and feed
-- polling on the owning plane with unique feed URLs, entry lookup indexes,
-- and plane-local retention audit staging (mirroring catalog 0015/0130/0135).
CREATE UNIQUE INDEX IF NOT EXISTS subscription_library_feed_idx
  ON subscription(library_id, feed_url);
CREATE INDEX IF NOT EXISTS subscription_entry_media_idx
  ON subscription_entry(media_id);
CREATE INDEX IF NOT EXISTS subscription_entry_download_path_idx
  ON subscription_entry(download_path) WHERE download_path <> '';
CREATE TABLE IF NOT EXISTS subscription_retention_activity (
  operation_id TEXT PRIMARY KEY,
  media_id TEXT NOT NULL,
  library_id TEXT NOT NULL,
  occurred_at_ms INTEGER NOT NULL
) WITHOUT ROWID;
