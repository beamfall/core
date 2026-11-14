-- CORE-1194 — per-Profile watchlist and personal rating.
--
-- Both tables key on (profile, target kind, target id) so one row shape covers
-- a Media item and an Entity. They live in the events DB with the other
-- per-Profile engagement state (playback_progress, media_favorite) and are
-- deleted with the Profile and pruned when the target leaves the catalog.
CREATE TABLE watchlist_item (
  profile_id TEXT NOT NULL,
  target_kind TEXT NOT NULL CHECK (target_kind IN ('media', 'entity')),
  target_id TEXT NOT NULL,
  added_at_ms INTEGER NOT NULL,
  PRIMARY KEY (profile_id, target_kind, target_id)
);

CREATE INDEX watchlist_item_recent ON watchlist_item(profile_id, added_at_ms DESC);

CREATE TABLE item_rating (
  profile_id TEXT NOT NULL,
  target_kind TEXT NOT NULL CHECK (target_kind IN ('media', 'entity')),
  target_id TEXT NOT NULL,
  rating INTEGER NOT NULL CHECK (rating BETWEEN 1 AND 10),
  rated_at_ms INTEGER NOT NULL,
  PRIMARY KEY (profile_id, target_kind, target_id)
);

CREATE INDEX item_rating_ranked ON item_rating(profile_id, rating DESC, rated_at_ms DESC);
