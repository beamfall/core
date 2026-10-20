-- Admin API tokens (ADR-0013 §5): long-lived, Console-created, ops-class
-- credentials for the CLI and automation. Only the SHA-256 hash of the
-- secret is at rest (the secret is 256-bit random — no KDF needed); the
-- full token string is shown exactly once, at creation. Tokens are never
-- granted Reveal: they authenticate the ops surface with hidden-world
-- visibility only — the Reveal contract is per-session and PIN-gated
-- (ADR-0004), and a standing credential must not be a standing reveal.
CREATE TABLE IF NOT EXISTS api_token (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  secret_hash TEXT NOT NULL,
  created_at_ms INTEGER NOT NULL,
  last_used_at_ms INTEGER NOT NULL DEFAULT 0,
  revoked INTEGER NOT NULL DEFAULT 0
);
