-- CORE-3312 (ADR-0240 D5, D8): server-side originator attribution for Player
-- display play-media commands, playback grants bound to originator and player
-- binding generation, and per-(grant, sequence) idempotent report ledger.
-- Off the entity-query hot path.

ALTER TABLE player_commanded_media
  ADD COLUMN originator_profile_id TEXT NOT NULL DEFAULT '';
ALTER TABLE player_commanded_media
  ADD COLUMN originator_session_id TEXT NOT NULL DEFAULT '';
ALTER TABLE player_commanded_media
  ADD COLUMN player_binding_generation INTEGER NOT NULL DEFAULT 0;
ALTER TABLE player_commanded_media
  ADD COLUMN active_for_attribution INTEGER NOT NULL DEFAULT 0;

CREATE TABLE playback_grant (
  grant_id TEXT NOT NULL PRIMARY KEY,
  session_id TEXT NOT NULL,
  device_id TEXT NOT NULL DEFAULT '',
  profile_id TEXT NOT NULL,
  media_id TEXT NOT NULL,
  originator_profile_id TEXT NOT NULL DEFAULT '',
  originator_session_id TEXT NOT NULL DEFAULT '',
  player_binding_generation INTEGER NOT NULL DEFAULT 0,
  walk_up INTEGER NOT NULL DEFAULT 0 CHECK (walk_up IN (0, 1)),
  ambient INTEGER NOT NULL DEFAULT 0 CHECK (ambient IN (0, 1)),
  last_position_seconds INTEGER NOT NULL DEFAULT 0,
  last_duration_seconds INTEGER NOT NULL DEFAULT 0,
  created_at_ms INTEGER NOT NULL,
  expires_at_ms INTEGER NOT NULL,
  ended_at_ms INTEGER,
  cancelled_at_ms INTEGER
);

CREATE INDEX idx_playback_grant_session_media
  ON playback_grant (session_id, media_id, created_at_ms DESC);
CREATE INDEX idx_playback_grant_device
  ON playback_grant (device_id);
CREATE INDEX idx_playback_grant_expires
  ON playback_grant (expires_at_ms);

CREATE TABLE playback_grant_report (
  grant_id TEXT NOT NULL REFERENCES playback_grant(grant_id) ON DELETE CASCADE,
  seq INTEGER NOT NULL CHECK (seq >= 0),
  received_at_ms INTEGER NOT NULL,
  PRIMARY KEY (grant_id, seq)
) WITHOUT ROWID;
