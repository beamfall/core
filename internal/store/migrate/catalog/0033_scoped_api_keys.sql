-- Scoped API keys (ADR-0040 §2.2): named, revocable third-party developer
-- credentials carrying a subset of a closed five-scope vocabulary
-- (read-catalog, read-sessions, ingest, events-subscribe, playback-control)
-- and an optional Profile binding for Reveal filtering. Distinct from the
-- ADR-0013 admin api_token table (operator-only, unrestricted). Only the
-- SHA-256 hash of the secret is at rest (the secret is 256-bit random — no KDF
-- needed); the full key string is shown exactly once, at creation. Scopes are
-- stored as a comma-separated list from the closed vocabulary; the server
-- validates membership on write, so a row can never carry an unknown scope.
CREATE TABLE IF NOT EXISTS scoped_api_key (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  secret_hash TEXT NOT NULL,
  scopes TEXT NOT NULL,
  profile_id TEXT REFERENCES profile(id),
  created_at_ms INTEGER NOT NULL,
  last_used_at_ms INTEGER NOT NULL DEFAULT 0,
  revoked INTEGER NOT NULL DEFAULT 0
);
