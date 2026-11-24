-- CORE-3313 / ADR-0240 D10: bind party_screen rows to their owning auth
-- Session and track joined player displays (device_id, binding_generation,
-- chosen_lead) for eligibility, controller liveness, and release.
ALTER TABLE party_screen ADD COLUMN auth_session_id TEXT NOT NULL DEFAULT '';
ALTER TABLE party_screen ADD COLUMN device_id TEXT NOT NULL DEFAULT '';
ALTER TABLE party_screen ADD COLUMN binding_generation INTEGER NOT NULL DEFAULT 0;
ALTER TABLE party_screen ADD COLUMN chosen_lead INTEGER NOT NULL DEFAULT 0 CHECK (chosen_lead IN (0, 1));

CREATE INDEX idx_party_screen_auth_session ON party_screen (auth_session_id);
CREATE INDEX idx_party_screen_device ON party_screen (device_id) WHERE dropped_at_ms IS NULL AND device_id <> '';
