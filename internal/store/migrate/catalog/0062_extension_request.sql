-- Parental extension request (ADR-0039 §2.1–2.4 / PUSH-2). When a managed
-- Profile is blocked mid-show (access-schedule expiry, stream cap, or viewing
-- budget — §2.1: "all three, single request shape") it raises a pending
-- extension_request. An adult Profile approves or denies it from a push
-- notification; the first responder wins (§2.3 step 4) and a 5-minute window
-- (§2.3 step 5) auto-expires an unanswered request. On approval the managed
-- Profile's block is lifted for the granted window, scoped to the requesting
-- Session only (§2.2 — "scoped to the current Session; does not modify the
-- schedule permanently").
--
-- The supervisor relationship is the household model ADR-0039 §2.3 names: the
-- request fans out to "every adult Profile on the server" — there is no stored
-- supervisor→managed link, and the rejected-alternatives section pins it ("the
-- first available parent answers, not a designated primary"). So this table has
-- NO supervisor column: any adult Profile is an authorized responder, and a
-- managed Profile can never respond (it is not adult). responder_profile_id
-- records which adult resolved it, append-only after the first response.
--
-- requesting_session_id is the §2.2 "current Session" the grant is scoped to:
-- auth.Resolve consults an approved request for THIS session (not the Profile at
-- large) when deciding whether to lift the block, so an extension never leaks
-- across a Profile's other Sessions. It is the "requesting context" the request
-- carries; the trigger source rides trigger_source for the §2.1 notification
-- label ("schedule ended" vs "daily limit"), opaque to Core (invariant #1).
--
-- status is a closed four-value vocabulary (pending | approved | denied |
-- expired); grant_minutes is the resolved extension length (the §2.2
-- finish-episode remaining runtime, or 15/30/60); grant_expires_at_ms is when an
-- approved grant's window closes — after it, normal enforcement resumes even
-- though the request row stays (append-only past the FREEZE-4 marker, ADR-0011
-- §2). No media title, position, or Profile name is stored beyond the opaque
-- ids and the closed-vocabulary trigger source — the push payload (§2.5) carries
-- only {type, eventId}; details are fetched over the authenticated API.
--
-- Append-only past the FREEZE-4 marker (ADR-0011 §2): adding this migration is a
-- deliberate act — schemaFreezeCatalogMigrations bumps 59 → 60 in the same
-- commit and `make bench` re-confirms the P95 budgets.
CREATE TABLE IF NOT EXISTS extension_request (
  id                    TEXT PRIMARY KEY,
  profile_id            TEXT NOT NULL REFERENCES profile(id) ON DELETE CASCADE,
  requesting_session_id TEXT NOT NULL,
  trigger_source        TEXT NOT NULL DEFAULT '',
  status                TEXT NOT NULL,
  remaining_minutes     INTEGER NOT NULL DEFAULT 0,
  grant_minutes         INTEGER NOT NULL DEFAULT 0,
  created_at_ms         INTEGER NOT NULL,
  expires_at_ms         INTEGER NOT NULL,
  grant_expires_at_ms   INTEGER NOT NULL DEFAULT 0,
  responder_profile_id  TEXT NOT NULL DEFAULT ''
);

-- The block hook's grant lookup (auth.Resolve) keys on the managed Profile's
-- approved requests for one Session; the per-Profile pending-request guard and
-- the once-per-session pre-authorization check key on profile_id too. The index
-- keeps both reads off a full-table scan as request rows accumulate.
CREATE INDEX IF NOT EXISTS idx_extension_request_profile
  ON extension_request(profile_id, requesting_session_id, status);

-- Per-managed-Profile pre-authorization (ADR-0039 §2.4): an adult configures
-- "automatic ONE extension per session, no confirmation". When set, the first
-- extension request in a Session is auto-approved for finish-episode; subsequent
-- requests in the same Session still require adult approval. It is a per-Profile
-- toggle (one row per managed Profile, present only when enabled), distinct from
-- the access schedule (it is not a blanket override — only the first per
-- Session). ON DELETE CASCADE drops it with the Profile.
CREATE TABLE IF NOT EXISTS extension_preauthorization (
  profile_id    TEXT PRIMARY KEY REFERENCES profile(id) ON DELETE CASCADE,
  enabled       INTEGER NOT NULL DEFAULT 0,
  updated_at_ms INTEGER NOT NULL
);
