-- CORE-0706: Encrypted Library holding tables for feed Subscription state and
-- item-state imports written to catalog.db before Encrypted Libraries refused
-- Subscriptions. Feed URLs, auth headers, entry GUIDs, enclosure URLs and paths
-- are private to the Library, so the next unseal moves those legacy rows here
-- instead of leaving them in the shared catalog or dropping them. Nothing
-- writes these tables afterwards; Subscriptions stay refused for Encrypted
-- Libraries.
CREATE TABLE subscription (
  id TEXT PRIMARY KEY,
  library_id TEXT NOT NULL,
  feed_url TEXT NOT NULL,
  root TEXT NOT NULL,
  title TEXT NOT NULL DEFAULT '',
  added_at_ms INTEGER NOT NULL,
  last_polled_at_ms INTEGER NOT NULL DEFAULT 0,
  etag TEXT NOT NULL DEFAULT '',
  last_modified TEXT NOT NULL DEFAULT '',
  retention_kind TEXT NOT NULL DEFAULT 'keep-all',
  retention_n INTEGER,
  auth_header TEXT NOT NULL DEFAULT '',
  auth_ref TEXT NOT NULL DEFAULT '',
  last_error_class TEXT NOT NULL DEFAULT '',
  failing_since_ms INTEGER NOT NULL DEFAULT 0,
  consecutive_failures INTEGER NOT NULL DEFAULT 0,
  next_poll_at_ms INTEGER NOT NULL DEFAULT 0,
  retired_at_ms INTEGER NOT NULL DEFAULT 0,
  moved_candidate_url TEXT NOT NULL DEFAULT '',
  moved_observations INTEGER NOT NULL DEFAULT 0,
  moved_at_ms INTEGER NOT NULL DEFAULT 0,
  refresh_requested_at_ms INTEGER NOT NULL DEFAULT 0,
  download_policy TEXT NOT NULL DEFAULT 'new-only',
  download_latest_n INTEGER NOT NULL DEFAULT 3
);

CREATE TABLE subscription_seen_entry (
  subscription_id TEXT NOT NULL,
  guid TEXT NOT NULL,
  seen_at_ms INTEGER NOT NULL,
  path TEXT NOT NULL DEFAULT '',
  feed_position INTEGER,
  PRIMARY KEY (subscription_id, guid)
);

CREATE TABLE subscription_entry (
  subscription_id TEXT NOT NULL,
  guid TEXT NOT NULL,
  media_id TEXT NOT NULL,
  feed_position INTEGER NOT NULL DEFAULT 0,
  published_at_ms INTEGER NOT NULL DEFAULT 0,
  first_seen_at_ms INTEGER NOT NULL,
  enclosure_url TEXT NOT NULL DEFAULT '',
  enclosure_type TEXT NOT NULL DEFAULT '',
  enclosure_length INTEGER NOT NULL DEFAULT 0,
  download_state TEXT NOT NULL DEFAULT 'none',
  download_path TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (subscription_id, guid)
);

CREATE TABLE subscription_entry_collision (
  subscription_id TEXT NOT NULL,
  guid TEXT NOT NULL,
  bound_path TEXT NOT NULL,
  incoming_path TEXT NOT NULL,
  incoming_media_id TEXT NOT NULL,
  first_seen_at_ms INTEGER NOT NULL,
  last_seen_at_ms INTEGER NOT NULL,
  observations INTEGER NOT NULL DEFAULT 1,
  PRIMARY KEY (subscription_id, guid, incoming_path)
) WITHOUT ROWID;

CREATE TABLE item_state_import (
  profile_id TEXT NOT NULL,
  subscription_id TEXT NOT NULL,
  source TEXT NOT NULL DEFAULT '',
  item_guid TEXT NOT NULL,
  played INTEGER NOT NULL DEFAULT 0,
  has_progress INTEGER NOT NULL DEFAULT 0,
  progress REAL NOT NULL DEFAULT 0,
  starred INTEGER NOT NULL DEFAULT 0,
  imported_at_ms INTEGER NOT NULL,
  applied_at_ms INTEGER NOT NULL DEFAULT 0,
  applied_media_id TEXT NOT NULL DEFAULT '',
  import_run_id TEXT,
  import_importer_id TEXT,
  import_match_method TEXT,
  PRIMARY KEY (profile_id, subscription_id, item_guid)
);

-- Cleared by the next unseal once catalog.db no longer holds the rows.
INSERT OR IGNORE INTO encrypted_library_metadata (key, value)
  VALUES ('legacy_subscription_evacuation', 'pending');
