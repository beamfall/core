# Live sessions end, scans share the queue, play history counts real plays

Tickets: CORE-2305, CORE-2306, CORE-2307, CORE-2309, CORE-2316, CORE-2317 (`queue:core:main`)

## Why

A tower QA pass found six server faults:

- `liveSessions` listed playbacks whose players had closed. A row stayed until its stream token
  expired, and `transcoding` could say true for a remux.
- Scheduled jobs waited behind long scans. A yielded scan was stamped with the time it was
  claimed, so it sorted ahead of jobs queued while it ran. That breaks the existing requirement
  "Jobs of one priority take turns".
- `jobEvents` returned every event a job ever wrote. A long scan writes thousands of progress
  events.
- A scan of a missing root walked nothing and said nothing.
- Play history counted a 0-second open as a play and trusted a client's watched flag alone, so
  plays were marked Finished seconds in.
- The startup log printed the automatic transcode cap even when an env cap was in force.

## What changes

- New mutation `endPlayback(mediaId: ID!): Boolean!`. It ends the calling device's live session
  for that item and returns true, including when there is nothing to end.
- The stream-token ledger records when each playback was last active (events migration 0023).
  Issuing a token or reporting progress refreshes it. `liveSessions` leaves out playbacks idle for
  longer than two minutes. `transcoding` is true exactly when `playMethod` is `transcode`.
- A job transition stamps the later of the job's last update and now, so a yield after a long run
  queues behind the work that arrived during it. A scan job also yields after two minutes of work,
  not only after its file batch.
- A job keeps only its latest progress event; the outbox still carries every update.
  `jobEvents(id, limit, beforeID)` returns the newest `limit` events (default 200, max 1000) in
  time order. `beforeID` reads further back.
- A scan whose root is missing or unreadable fails at once. The job emits a
  `scan-root-unavailable` event with `{libraryID, root, reason}`, where reason is `missing` or
  `unreadable`. Clients use this event to raise their own attention signal.
- A derived play must last at least 30 seconds, or a quarter of an item shorter than two minutes.
  A play is Finished only when the item is reported watched and playback got within 20% or 30
  seconds of the end.
- The startup log reports the effective transcode cap, its source (`env/flag`, `console` or
  `automatic`) and the automatic cap.

## Non-goals

- Other readers of the ledger (webhook events, device lists, session control, `/v1` sessions) keep
  their current unfiltered view.
- Stream tokens stay valid until they expire. The ledger only reports playback; it does not
  enforce access.
- Write-time watched semantics are unchanged. Only the derived history view changes.

## Failure modes

- A legacy ledger row (activity 0) counts as idle until its player reports progress or fetches a
  new token.
- A paused player that sends no progress drops out of `liveSessions` after two minutes and comes
  back on its next report.
- `endPlayback` without a device session, or for an item with no live row, returns true and
  changes nothing.
