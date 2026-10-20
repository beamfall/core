CREATE TABLE IF NOT EXISTS beam_handoff_offer (
  token_hash TEXT PRIMARY KEY,
  source_session_id TEXT NOT NULL REFERENCES auth_session(id) ON DELETE CASCADE,
  source_device_id TEXT NOT NULL REFERENCES device(id) ON DELETE CASCADE,
  source_profile_id TEXT NOT NULL REFERENCES profile(id) ON DELETE CASCADE,
  target_device_id TEXT REFERENCES device(id) ON DELETE CASCADE,
  media_id TEXT NOT NULL REFERENCES media(id) ON DELETE CASCADE,
  position_seconds INTEGER NOT NULL,
  expires_at_ms INTEGER NOT NULL,
  created_at_ms INTEGER NOT NULL,
  claimed_at_ms INTEGER
);

CREATE INDEX IF NOT EXISTS idx_beam_handoff_source_open
  ON beam_handoff_offer(source_session_id, claimed_at_ms);

CREATE INDEX IF NOT EXISTS idx_beam_handoff_expires
  ON beam_handoff_offer(expires_at_ms);
