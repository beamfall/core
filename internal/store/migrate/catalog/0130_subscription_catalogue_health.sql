-- Feed catalogue and download policy (ADR-0238, CORE-2953).
-- Existing Subscriptions keep their behaviour of downloading new entries
-- only: the column default is 'new-only'.
ALTER TABLE subscription ADD COLUMN download_policy TEXT NOT NULL DEFAULT 'new-only'
  CHECK (download_policy IN ('latest','new-only','everything','stream-only'));
ALTER TABLE subscription ADD COLUMN download_latest_n INTEGER NOT NULL DEFAULT 3;

-- One row per feed entry with an enclosure, bound to the entry's Media
-- (file-less until downloaded). enclosure_url is secret class like
-- subscription.feed_url (invariant 5): never logged, never on the wire.
CREATE TABLE subscription_entry (
  subscription_id TEXT NOT NULL REFERENCES subscription(id) ON DELETE CASCADE,
  guid TEXT NOT NULL,
  media_id TEXT NOT NULL REFERENCES media(id) ON DELETE CASCADE,
  feed_position INTEGER NOT NULL DEFAULT 0,
  published_at_ms INTEGER NOT NULL DEFAULT 0,
  first_seen_at_ms INTEGER NOT NULL,
  enclosure_url TEXT NOT NULL DEFAULT '',
  enclosure_type TEXT NOT NULL DEFAULT '',
  enclosure_length INTEGER NOT NULL DEFAULT 0,
  download_state TEXT NOT NULL DEFAULT 'none'
    CHECK (download_state IN ('none','queued','downloaded','failed')),
  download_path TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (subscription_id, guid)
);
CREATE INDEX subscription_entry_media_idx ON subscription_entry(media_id);
CREATE INDEX subscription_entry_download_path_idx ON subscription_entry(download_path)
  WHERE download_path <> '';
