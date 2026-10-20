-- Server admin password (ADR-0007 §4, ADR-0012 §6): one per server, set at
-- first run, gates the Admin Session tier that authorizes Console operations.
-- The password is low-entropy (a human-chosen secret), so a leaked catalog.db
-- must not expose it to an offline brute force: it is salted with a random
-- value and stretched with argon2id, mirroring the per-Profile reveal-PIN
-- verifier (internal/store/reveal.go). Exactly one row (id = 'server'); the
-- secret itself is never stored, only the salted argon2id verifier.
CREATE TABLE IF NOT EXISTS admin_credential (
  id TEXT PRIMARY KEY,
  password_hash TEXT NOT NULL,
  password_salt BLOB NOT NULL,
  updated_at_ms INTEGER NOT NULL DEFAULT 0
);

-- The Admin Session binds to the cookie/auth session (ADR-0012 §5): admin
-- authorization is a short-lived window stamped on the session row, exactly
-- like the Reveal contract's `revealed` switch. NULL or a past timestamp means
-- the session holds no admin authorization; every Console-op read re-derives
-- this from the row, so revocation and expiry are immediate. The admin window
-- is orthogonal to Device Role (ADR-0007 §4).
ALTER TABLE auth_session ADD COLUMN admin_until_ms INTEGER;
