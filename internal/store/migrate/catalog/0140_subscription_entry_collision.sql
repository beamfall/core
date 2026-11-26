-- CORE-0706 (COREAI-PRIVATE-INGEST-PLANE-1, slice 3): a feed entry GUID that
-- already names one file and is then offered for a different file is kept for
-- operator review instead of being dropped with an error. The GUID is stored
-- exactly as the feed sent it. A repeat offer of the same file refreshes the
-- one row and counts the observation.
CREATE TABLE subscription_entry_collision (
  subscription_id TEXT NOT NULL REFERENCES subscription(id) ON DELETE CASCADE,
  guid TEXT NOT NULL,
  bound_path TEXT NOT NULL,
  incoming_path TEXT NOT NULL,
  incoming_media_id TEXT NOT NULL REFERENCES media(id) ON DELETE CASCADE,
  first_seen_at_ms INTEGER NOT NULL,
  last_seen_at_ms INTEGER NOT NULL,
  observations INTEGER NOT NULL DEFAULT 1,
  PRIMARY KEY (subscription_id, guid, incoming_path)
) WITHOUT ROWID;
