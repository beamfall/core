-- Stream Token ledger (ADR-0036 My Data: read-only view of outstanding Stream
-- Tokens). Stream Tokens are stateless signed grants (ADR-0010 §3) — verified
-- by signature on every byte request, never stored for access control. This
-- ledger is a SEPARATE, transparency-only record so the My Data panel can show
-- a Profile its own outstanding tokens; it is never consulted to authorize
-- bytes. It records the target KIND and expiry only, plus the media id so the
-- read can reveal-filter (a token for hidden Media is reported with kind only,
-- never an identifying field). Rows are keyed by (session_id, target,
-- media_id) and upserted on issue, so re-issuing the same grant refreshes
-- expiry instead of accumulating duplicates.
CREATE TABLE stream_token_ledger (
  session_id TEXT NOT NULL,
  profile_id TEXT NOT NULL,
  device_id TEXT NOT NULL DEFAULT '',
  target TEXT NOT NULL,
  media_id TEXT NOT NULL,
  restricted INTEGER NOT NULL DEFAULT 0,
  expires_at_ms INTEGER NOT NULL,
  PRIMARY KEY (session_id, target, media_id)
);

CREATE INDEX idx_stream_token_ledger_profile
  ON stream_token_ledger (profile_id, expires_at_ms);
