# endPlayback stops the transcode, and revocation still finds it

Ticket: CORE-2345 (`queue:core:main`).

## Why

`endPlayback` (CORE-2305) deletes the caller's stream-token ledger rows for the item, so the
live-sessions dashboard drops the playback at once. It never stopped the session's transcode, so
FFmpeg kept running and held a transcode slot until the five-minute idle reap. Device revocation
then made it worse: `revokeDevice` and `revokeAllDevices` found a device's transcodes only through
that ledger, so a transcode that outlived its ledger rows was never aborted. On the tower a revoked
Apple TV kept its transcode running and the mutation reported `transcodesAborted: 0`.

## What changes

- `endPlayback` also stops the calling session's transcode of that item, through a new
  `TerminatePlayback(session, media)` on the playback session manager. The session's other items
  and other sessions are untouched.
- `revokeDevice` and `revokeAllDevices` find a device's sessions from its auth session rows as well
  as from the ledger, so a transcode is aborted even after its ledger rows are gone.
- My Data's stream-token list, which the ticket also names, is not changed here.
