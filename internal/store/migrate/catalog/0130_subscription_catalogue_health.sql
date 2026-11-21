-- Feed catalogue, download policy and feed health (ADR-0238, CORE-2953/2955).
-- Existing Subscriptions keep their behaviour of downloading new entries
-- only: the column default is 'new-only'.
ALTER TABLE subscription ADD COLUMN download_policy TEXT NOT NULL DEFAULT 'new-only'
  CHECK (download_policy IN ('latest','new-only','everything','stream-only'));
ALTER TABLE subscription ADD COLUMN download_latest_n INTEGER NOT NULL DEFAULT 3;
ALTER TABLE subscription ADD COLUMN last_error_class TEXT NOT NULL DEFAULT '';
ALTER TABLE subscription ADD COLUMN failing_since_ms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE subscription ADD COLUMN consecutive_failures INTEGER NOT NULL DEFAULT 0;
ALTER TABLE subscription ADD COLUMN next_poll_at_ms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE subscription ADD COLUMN last_success_at_ms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE subscription ADD COLUMN retired_at_ms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE subscription ADD COLUMN refresh_requested_at_ms INTEGER NOT NULL DEFAULT 0;
-- A feed move (permanent redirect or in-feed new-feed-url) rewrites feed_url
-- only after two consecutive polls observe the same new URL.
-- pending_feed_url is secret class like feed_url: never logged, never on the
-- wire.
ALTER TABLE subscription ADD COLUMN pending_feed_url TEXT NOT NULL DEFAULT '';
ALTER TABLE subscription ADD COLUMN pending_feed_url_count INTEGER NOT NULL DEFAULT 0;

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
