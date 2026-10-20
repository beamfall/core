CREATE TABLE play_queue (
  profile_id TEXT NOT NULL,
  position INTEGER NOT NULL,
  media_id TEXT NOT NULL,
  added_at_ms INTEGER NOT NULL,
  PRIMARY KEY (profile_id, position),
  UNIQUE (profile_id, media_id)
);
