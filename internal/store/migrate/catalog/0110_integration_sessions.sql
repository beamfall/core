PRAGMA legacy_alter_table = ON;

CREATE TABLE beam_handoff_offer_copy AS
SELECT * FROM beam_handoff_offer;

DROP TABLE beam_handoff_offer;

ALTER TABLE auth_session RENAME TO auth_session_legacy;

CREATE TABLE auth_session (
  id TEXT PRIMARY KEY,
  device_id TEXT REFERENCES device(id),
  profile_id TEXT NOT NULL REFERENCES profile(id),
  revealed INTEGER NOT NULL DEFAULT 0,
  expires_at_ms INTEGER,
  admin_until_ms INTEGER,
  kind TEXT CHECK (kind IS NULL OR kind IN ('enrolled', 'integration')),
  integration_scopes TEXT NOT NULL DEFAULT ''
);

INSERT INTO auth_session(
  id, device_id, profile_id, revealed, expires_at_ms, admin_until_ms, kind, integration_scopes
)
SELECT id, device_id, profile_id, revealed, expires_at_ms, admin_until_ms, NULL, ''
FROM auth_session_legacy;

DROP TABLE auth_session_legacy;

CREATE TABLE beam_handoff_offer (
  token_hash TEXT PRIMARY KEY,
  source_session_id TEXT NOT NULL REFERENCES auth_session(id) ON DELETE CASCADE,
  source_device_id TEXT NOT NULL REFERENCES device(id) ON DELETE CASCADE,
  source_profile_id TEXT NOT NULL REFERENCES profile(id) ON DELETE CASCADE,
  target_device_id TEXT REFERENCES device(id) ON DELETE CASCADE,
  media_id TEXT NOT NULL REFERENCES media(id) ON DELETE CASCADE,
  position_seconds INTEGER NOT NULL,
  expires_at_ms INTEGER NOT NULL,
  created_at_ms INTEGER NOT NULL,
  claimed_at_ms INTEGER,
  media_class TEXT NOT NULL DEFAULT '',
  sender_revealed INTEGER NOT NULL DEFAULT 0
);

INSERT INTO beam_handoff_offer(
  token_hash, source_session_id, source_device_id, source_profile_id,
  target_device_id, media_id, position_seconds, expires_at_ms, created_at_ms,
  claimed_at_ms, media_class, sender_revealed
)
SELECT token_hash, source_session_id, source_device_id, source_profile_id,
       target_device_id, media_id, position_seconds, expires_at_ms, created_at_ms,
       claimed_at_ms, media_class, sender_revealed
FROM beam_handoff_offer_copy;

DROP TABLE beam_handoff_offer_copy;

CREATE INDEX idx_beam_handoff_source_open
  ON beam_handoff_offer(source_session_id, claimed_at_ms);

CREATE INDEX idx_beam_handoff_expires
  ON beam_handoff_offer(expires_at_ms);

PRAGMA legacy_alter_table = OFF;

ALTER TABLE profile
  ADD COLUMN integration_sign_in INTEGER NOT NULL DEFAULT 0
  CHECK (integration_sign_in IN (0, 1));

CREATE INDEX auth_session_profile_kind_idx ON auth_session(profile_id, kind);
