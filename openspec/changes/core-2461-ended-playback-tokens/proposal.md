# Ended playback keeps its stream tokens on My Data

Ticket: CORE-2461 (`queue:core:main`), split out of CORE-2345.

## Why

`endPlayback` deletes the caller's rows from the stream-token ledger so the live-sessions
dashboard drops the playback at once (CORE-2305). The ledger is also My Data's only record of a
Profile's outstanding Stream Tokens (ADR-0036). Stream Tokens are stateless signed grants
(ADR-0010 §3): deleting the row does not revoke the grant, which still authorises bytes until it
expires. So once a player closes, My Data stops listing a grant that is still valid, and
under-reports what is outstanding.

## What Changes

- `endPlayback` marks the caller's media-byte ledger rows for that item as ended instead of
  deleting them. The mark lives in the existing `last_active_ms` column (a negative value), so no
  migration and no schema-freeze bump are needed.
- The active-session reads (`liveSessions` through `ActiveSessions`, and the `/metrics`
  active-session gauge `ActiveSessionCount`) skip ended rows, so the dashboard and the gauge still
  drop the playback at once.
- A progress report does not revive an ended row; a new grant for the item (a new playback)
  does, because issuing a grant rewrites the row.
- My Data's stream-token read is unchanged: it lists every unexpired row, ended or not.
- Expiry sweeps and session revocation still delete rows as before.

## Non-goals

- Revoking Stream Tokens on `endPlayback`. Grants stay stateless.
- Changing transcode teardown on `endPlayback` (CORE-2345).
