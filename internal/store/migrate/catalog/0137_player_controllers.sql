-- CORE-3311 / ADR-0240 D4, D9: Player Profile controllers list and display last-seen timestamp.
ALTER TABLE profile ADD COLUMN player_controllers_all_adults INTEGER NOT NULL DEFAULT 1;
ALTER TABLE device ADD COLUMN last_seen_at_ms INTEGER NOT NULL DEFAULT 0;

CREATE TABLE player_controller (
  player_profile_id TEXT NOT NULL REFERENCES profile(id) ON DELETE CASCADE,
  profile_id TEXT NOT NULL REFERENCES profile(id) ON DELETE CASCADE,
  mode TEXT NOT NULL CHECK (mode IN ('include', 'exclude')),
  PRIMARY KEY (player_profile_id, profile_id)
);

CREATE INDEX idx_player_controller_profile
  ON player_controller (profile_id);
