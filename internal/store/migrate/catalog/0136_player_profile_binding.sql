-- CORE-3310 / ADR-0240 D1, D2, D7: durable Device player binding and per-Player Profile walk-up toggle.
ALTER TABLE device ADD COLUMN player_profile_id TEXT REFERENCES profile(id) ON DELETE SET NULL;
ALTER TABLE device ADD COLUMN player_display_name TEXT NOT NULL DEFAULT '';
ALTER TABLE device ADD COLUMN player_binding_generation INTEGER NOT NULL DEFAULT 0;

CREATE INDEX idx_device_player_profile
  ON device (player_profile_id)
  WHERE player_profile_id IS NOT NULL;

ALTER TABLE profile ADD COLUMN walk_up_enabled INTEGER NOT NULL DEFAULT 0;
