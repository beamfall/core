-- CORE-3310 / ADR-0240 D7: media IDs named by play-media session-control
-- commands addressed to a player-bound Session, used to enforce the 12-hour
-- walk-up-off Media read window.
CREATE TABLE player_commanded_media (
  session_id TEXT NOT NULL,
  media_id TEXT NOT NULL,
  commanded_at_ms INTEGER NOT NULL,
  PRIMARY KEY (session_id, media_id)
) WITHOUT ROWID;

CREATE INDEX idx_player_commanded_media_session
  ON player_commanded_media (session_id, commanded_at_ms);
