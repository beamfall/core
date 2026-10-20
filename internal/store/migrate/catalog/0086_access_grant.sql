-- ACCESS-3 (ADR-0113 §2.3): per-title access grant. When an adult resolves
-- an AccessRequest with verdict="granted", an access_grant row is upserted
-- atomically in the same transaction. The grant persists until an adult
-- revokes it; revocation deletes the row. Presence of a row is the grant
-- predicate — no status column is needed.
CREATE TABLE IF NOT EXISTS access_grant (
  id               TEXT PRIMARY KEY,
  profile_id       TEXT NOT NULL REFERENCES profile(id) ON DELETE CASCADE,
  media_id         TEXT NOT NULL REFERENCES media(id)   ON DELETE CASCADE,
  request_id       TEXT NOT NULL DEFAULT '',
  actor_profile_id TEXT NOT NULL DEFAULT '',
  created_at_ms    INTEGER NOT NULL,
  updated_at_ms    INTEGER NOT NULL
);

-- One grant per (profile, media) pair — a second grant attempt refreshes the
-- existing row (ON CONFLICT DO UPDATE) rather than creating a duplicate.
CREATE UNIQUE INDEX IF NOT EXISTS uniq_access_grant_profile_media
  ON access_grant(profile_id, media_id);

-- Fast lookup of all grants for a profile (scope resolution + Console UI).
CREATE INDEX IF NOT EXISTS idx_access_grant_profile
  ON access_grant(profile_id);
