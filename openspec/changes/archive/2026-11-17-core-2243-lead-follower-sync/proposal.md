# Lead and follower screens on one timeline

Tickets: CORE-2243, CORE-2244, CORE-2245 (`queue:core:main`). Decisions: CORE-2247. Follows
CORE-1677, CORE-1763, CORE-1766. Design: ADR-0035, ADR-0038, ADR-0056.

## Why

A room with several screens and one sound system needs every picture to match the one audio
source. Watch Party keeps players within a few hundred milliseconds of a server timeline, corrects
all of them equally, and starts each change two seconds in the future. That is right for people
watching in different rooms. It is wrong when one player feeds the speakers: that player must
never be nudged, and the others must follow what is actually audible, including when the audio
player stalls or changes speed.

## What Changes

- **Conductor, lead party (CORE-2243):** a Watch Party can be formed as a lead party. Its host
  session is the lead: it owns the audio and is never corrected. Every other member is a follower.
  The lead reports the position it is audibly at and the server instant of that sample. When the
  report is more than 40 ms from the shared timeline, or the lead's speed changed, the timeline is
  re-anchored on the report and re-stated to every member. The timeline is also re-stated at least
  every 5 seconds while the lead reports. A lead party has no pre-buffer window, is never relaxed,
  refuses follower transport, keeps its invites after an accept so a follower that reloads joins
  again, and ends when the lead leaves instead of handing the host role to a follower.
- **Sync channel:** `GroupSyncPayload` gains additive `lead` and `ratePermille` fields on
  `transition` events. Clients that do not know them ignore them.
- **GraphQL (argument additions only):** `createWatchParty(lead: Boolean)` forms a lead party.
  `reportDrift(positionMS: Int, atMS: Int, ratePermille: Int)` carries the lead's report. Result
  shapes are unchanged, so there is no wire-contract change.
- **Clock (CORE-2244):** the calibration exchange carries fractional milliseconds. The web client
  takes several exchanges, keeps the offset from the one with the lowest round trip, and
  re-estimates while a party is active. Sync events are already delivered on commit (the event
  stream wakes on the outbox change signal), which this change relies on and cites.
- **Web (CORE-2245):** starting offers a lead party. The lead's player plays freely and reports
  its position about once a second. A follower's player is muted, follows the timeline at its
  rate, measures every 100 ms, holds within 40 ms, applies a per-screen display delay trim set on
  a calibration control, and rejoins on its own after a reload or a dropped connection.
- **CORE-0531:** unchanged. Create and accept still refuse callers outside the home network, and
  the household caps still apply. Sync calibration stays on the local network (CORE-2247).

## Impact

- Code: `internal/conductor`, `internal/graph` (Watch Party, `reportDrift`), `internal/store`
  (group sync payload), `internal/server` (calibration), and the web viewer player and strings.
- No migration. Parties stay ephemeral and in memory.
- Wire contract: no change. Only arguments and payload fields were added.
