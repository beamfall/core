# Per-Library rendition precompute Schedule that ships disabled

Ticket: CORE-2326 (`queue:core:main`).

## Why

Precomputed playback renditions (ADR-0057) could only be generated one Media at a time from the
Console. An operator who wants a Library's renditions ready ahead of playback had no way to ask for
it. Owner decision (2026-11-17): add one precompute Schedule per Library, shipped disabled, that the
operator turns on per Library in the Console.

## ADR-0057 reading

ADR-0057 §2 allows renditions only from explicit triggers (operator, Schedule, Source/Collection
rule, event, display setup), never from popularity, and never silently filling disks. A Schedule
the server enabled on its own would be an implicit trigger. So the Schedule is seeded disabled and
no Reconcile ever enables it: the operator enabling it is the explicit trigger. Once enabled it is
an ordinary Schedule trigger, and it stays inside the same cache budget the eviction job governs.

## What Changes

- `schedule.Spec` gains `DefaultDisabled`. Reconcile seeds such a row disabled. It still never
  changes an existing row's enabled flag, so a re-registered, orphaned or restarted Schedule keeps
  whatever the operator chose.
- One `playback-precompute:<libraryID>` Schedule (job kind `playback.precompute`, default daily at
  02:00) per live Library, declared only when a rendition cache budget and root are configured.
- A run queues Background-class precompute jobs for that Library's Media. It skips renditions
  already ready or generating, Media the precompute cannot back (images, refused or dimensionless
  sources, unknown duration), and Encrypted Libraries the server cannot generate for (no
  encrypted plane, or Sealed). It stops planning once the ready cache plus the planned jobs'
  size estimates would exceed the budget. Each job still passes the precompute admission governor
  when it runs, and Background jobs yield to playback like every other background job.
- No new UI: the Console Schedules list already lists every Schedule and toggles `enabled`. Its
  rows show the job kind but not which Library a per-Library Schedule belongs to; that label gap
  already applies to scan Schedules and is a separate Console change.
