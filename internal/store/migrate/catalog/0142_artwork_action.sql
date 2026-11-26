-- CORE-0706: an identification's artwork fetches run after its commit, so the
-- intent to fetch each slot is committed with the identification revision as a
-- pending action. The identification worker completes each action with a
-- compare-and-set from pending, and a restart replays every action still
-- pending, so each slot's artwork is applied exactly once per revision even if
-- the process dies between the metadata commit and the fetch. A newer revision
-- supersedes the Media's older pending actions. Rows leave with their revision
-- (ON DELETE CASCADE). The Encrypted Library mirror is encrypted-library 0019.
CREATE TABLE media_artwork_action (
  media_id TEXT NOT NULL,
  revision INTEGER NOT NULL,
  slot TEXT NOT NULL CHECK (slot <> ''),
  source_url TEXT NOT NULL CHECK (source_url <> ''),
  plugin_id TEXT NOT NULL,
  scraper_id TEXT NOT NULL,
  state TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'done', 'superseded')),
  outcome TEXT NOT NULL DEFAULT '',
  created_at_ms INTEGER NOT NULL,
  completed_at_ms INTEGER,
  PRIMARY KEY (media_id, revision, slot),
  FOREIGN KEY (media_id, revision) REFERENCES media_identification_revision(media_id, revision) ON DELETE CASCADE
);
CREATE INDEX media_artwork_action_pending ON media_artwork_action(state, created_at_ms) WHERE state = 'pending';
