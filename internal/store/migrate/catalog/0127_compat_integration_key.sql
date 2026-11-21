-- Durable Jellyfin compatibility integration keys (ADR-0171 §2.4, CORE-2725).
-- An admin mints a key in the Console for one opted-in adult Profile; tools
-- such as Seerr store it once and have no re-authentication flow. Only the
-- SHA-256 digest of the 256-bit random token is at rest (no KDF needed), and
-- the token is shown once, at mint. Scopes come from the closed compat
-- vocabulary minus compat.setup, validated on write. Revoked rows are kept so
-- the id stays resolvable for activity-log attribution; the rows go with
-- their Profile.
CREATE TABLE compat_integration_key (
  id TEXT PRIMARY KEY,
  profile_id TEXT NOT NULL REFERENCES profile(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  digest TEXT NOT NULL UNIQUE,
  scopes TEXT NOT NULL,
  created_at_ms INTEGER NOT NULL,
  last_used_at_ms INTEGER NOT NULL DEFAULT 0,
  revoked_at_ms INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX compat_integration_key_profile_idx ON compat_integration_key(profile_id, revoked_at_ms);
