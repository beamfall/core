CREATE TABLE circle_membership (
  id TEXT PRIMARY KEY,
  credential_generation INTEGER NOT NULL DEFAULT 1 CHECK (credential_generation > 0),
  revoked_at_ms INTEGER,
  CHECK (revoked_at_ms IS NULL OR revoked_at_ms >= 0)
);
CREATE TABLE circle_shadow_principal (
  id TEXT PRIMARY KEY,
  circle_id TEXT NOT NULL REFERENCES circle_membership(id) ON DELETE CASCADE,
  remote_profile_id TEXT NOT NULL,
  remote_profile_binding TEXT NOT NULL,
  policy_profile_id TEXT NOT NULL REFERENCES profile(id) ON DELETE RESTRICT,
  revealed INTEGER NOT NULL DEFAULT 0 CHECK (revealed IN (0, 1)),
  created_by_profile_id TEXT NOT NULL REFERENCES profile(id) ON DELETE RESTRICT,
  UNIQUE (circle_id, remote_profile_id)
);

CREATE TABLE circle_session_cache (
  circle_id TEXT NOT NULL REFERENCES circle_membership(id) ON DELETE CASCADE,
  session_id TEXT NOT NULL,
  shadow_principal_id TEXT NOT NULL REFERENCES circle_shadow_principal(id) ON DELETE CASCADE,
  negotiated_revision INTEGER NOT NULL CHECK (negotiated_revision >= 0),
  guest_spki_pin TEXT NOT NULL,
  PRIMARY KEY (circle_id, session_id)
);

CREATE TABLE circle_revoke_notice (
  id TEXT PRIMARY KEY,
  circle_id TEXT NOT NULL REFERENCES circle_membership(id) ON DELETE CASCADE,
  created_at_ms INTEGER NOT NULL CHECK (created_at_ms >= 0),
  attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
  acknowledged_at_ms INTEGER,
  UNIQUE (circle_id),
  CHECK (acknowledged_at_ms IS NULL OR acknowledged_at_ms >= created_at_ms)
);
