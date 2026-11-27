-- CORE-0706: Encrypted Library mirror of catalog 0141. An Encrypted Library's
-- pending artwork actions stay on its own plane, so no source URL or Media ID
-- for a private Media reaches the shared catalog.
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
