-- ACCESS-2: per-title access request (ADR-0113 §2.2). When a managed Profile
-- views a Locked item (ACCESS-1) and taps "Ask a parent", a pending
-- access_request is created for the (profile, media) pair. The request fans out
-- to every adult Profile's devices; the first adult to respond wins; an
-- unanswered request auto-expires after a configurable TTL (default 7 days).
--
-- DEDUP: only one pending request per (profile_id, media_id) pair is allowed at
-- a time, enforced by a partial unique index on status = 'pending'. Multiple
-- resolved rows are permitted — once resolved (granted/denied), a new pending
-- request for the same pair can be raised. On INSERT conflict, the caller
-- returns the existing pending row (idempotent tap).
--
-- VERDICT is a closed three-value vocabulary (granted | denied | suppressed):
-- 'granted' — adult approved, stored in access_grant (ACCESS-3); 'denied' —
-- adult refused; 'suppressed' — TTL expired before any response. An empty
-- verdict means the request is still pending. status is a two-value vocabulary
-- (pending | resolved); the resolved sub-state rides verdict. This keeps the
-- partial-index predicate (status = 'pending') cheap and stable.
--
-- No media title, Profile name, or other content rides the row — only the
-- opaque ids. The push payload (ADR-0039 §2.5 reuse) carries only {type, eventId};
-- adults fetch details over the authenticated API (invariant #5).
--
-- responder_profile_id records which adult resolved the request (empty while
-- pending). Any adult Profile is an authorized responder; there is no stored
-- supervisor link — the same household model as the extension request.
--
-- Append-only past the FREEZE-4 marker (ADR-0011 §2): schemaFreezeCatalogMigrations
-- bumps 82 → 83 in the same commit.
CREATE TABLE IF NOT EXISTS access_request (
  id                   TEXT PRIMARY KEY,
  profile_id           TEXT NOT NULL REFERENCES profile(id) ON DELETE CASCADE,
  media_id             TEXT NOT NULL REFERENCES media(id)   ON DELETE CASCADE,
  status               TEXT NOT NULL DEFAULT 'pending',
  verdict              TEXT NOT NULL DEFAULT '',
  created_at_ms        INTEGER NOT NULL,
  ttl_expires_at_ms    INTEGER NOT NULL,
  responder_profile_id TEXT NOT NULL DEFAULT ''
);

-- Dedup guard: at most one pending request per (profile, media) pair. Multiple
-- resolved rows are fine — the WHERE clause means the index only covers pending
-- rows, so INSERT on a resolved pair succeeds immediately.
CREATE UNIQUE INDEX IF NOT EXISTS uniq_access_request_pending
  ON access_request(profile_id, media_id) WHERE status = 'pending';

-- Inbox read + managed-Profile status check (child sees "Requested — waiting
-- for a parent") both key on profile_id + status; the adult inbox lists all
-- pending requests across profiles.
CREATE INDEX IF NOT EXISTS idx_access_request_profile
  ON access_request(profile_id, status);
