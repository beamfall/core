# Hold a lead party when the lead falls silent

Ticket: CORE-2266 (`queue:core:main`). Follows CORE-2243. Design: ADR-0038 §2.9.

## Why

A lead party's timeline keeps advancing between the lead's reports. When the lead stops reporting
without saying it paused (its player stalls without a stall event, its page freezes, its network
drops), every follower keeps playing and runs ahead of the audio until the lead's next report, or
until the lead's stream expires 20 seconds later.

## What Changes

- **Conductor:** a playing lead party whose lead has not reported for 3 seconds is held. The
  timeline stops at the position it had when the lead was last heard, and every member Profile
  gets a paused `transition`. The lead's next report, or its next transport, resumes the timeline
  on that report. A timeline the lead paused itself is still not moved by a report.
- **Sync channel and GraphQL:** no new fields or arguments. A hold is a paused lead `transition`.

## Impact

`internal/conductor`, `internal/graph`. No wire-contract, schema or migration change. Clients need
no change: a follower already pauses on a paused transition, and the lead's player is not touched.
