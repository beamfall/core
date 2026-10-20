-- Pack-subject Stream Token ledger (ADR-0098 area 6 + ADR-0036 My Data). The
-- generic stream_token_ledger (events 0011) keys rows by (session_id, target,
-- media_id) with media_id NOT NULL — a Pack-subject grant has no media_id, so it
-- cannot live there. This is the parallel transparency-only record for
-- Pack-subject grants so the My Data panel can show a Profile its own
-- outstanding Offline Pack tokens. Like the stream-token ledger it is NEVER
-- consulted to authorize bytes (the serving layer re-checks the signed grant +
-- live pack state + Reveal/seal) — it records target KIND, the pack id, and
-- expiry only. A token for a restricted pack is reported with kind/expiry only,
-- never an identifying field. Keyed by (session_id, target, pack_id) and
-- upserted on issue so re-issuing the same grant refreshes expiry instead of
-- accumulating duplicates.
CREATE TABLE pack_token_ledger (
  session_id TEXT NOT NULL,
  profile_id TEXT NOT NULL,
  device_id TEXT NOT NULL DEFAULT '',
  target TEXT NOT NULL,
  pack_id TEXT NOT NULL,
  restricted INTEGER NOT NULL DEFAULT 0,
  expires_at_ms INTEGER NOT NULL,
  PRIMARY KEY (session_id, target, pack_id)
);

CREATE INDEX idx_pack_token_ledger_profile
  ON pack_token_ledger (profile_id, expires_at_ms);
