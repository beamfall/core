CREATE TABLE IF NOT EXISTS playback_progress (
  profile_id TEXT NOT NULL,
  media_id TEXT NOT NULL,
  position_seconds INTEGER NOT NULL DEFAULT 0,
  duration_seconds INTEGER NOT NULL DEFAULT 0,
  watched INTEGER NOT NULL DEFAULT 0,
  updated_at_ms INTEGER NOT NULL,
  PRIMARY KEY (profile_id, media_id)
);

CREATE INDEX IF NOT EXISTS idx_playback_progress_profile_updated
  ON playback_progress (profile_id, updated_at_ms DESC);
