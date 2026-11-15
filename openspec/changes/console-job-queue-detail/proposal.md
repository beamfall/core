# Console job queue: job purpose, target Library folder and Scan now

Ticket: CORE-2109 (`queue:core:main`).

## Why

The Console Job queue lists rows such as "Scan", "Artwork sweep" or "Playback.precompute" with a
progress bar and nothing else. An operator cannot tell what a job does, which Library folder a
scan is walking, or how to force a scan from that page.

## What Changes

- Graph: `JobInfo` gains an optional `target { libraryID libraryTitle root backfill }` decoded from
  the job payload. It is filled only when the session may see that Library's operational detail
  (the same `jobNeedsRedaction` rule that already hides errors and progress) and the Library is
  visible to the session; otherwise it is omitted. Additive optional field; the development wire
  contract is published as `wire-contract-v2.12.0.json`. The registry pins 2.12.0; the field is
  optional, so consumers still on 2.11.0 keep working until they re-vendor.
- Web Console Job queue: each row shows one plain sentence for what the job kind does, and for
  library-scoped jobs the Library title and folder, with a "Metadata refresh" tag on backfill
  scans. Every server job kind has a label (dotted kinds no longer render as "Playback.precompute").
- Web Console Job queue: a Scan now control (the existing Rescan all libraries button) next to
  Refresh queues a scan of every library folder; folders that already have a scan queued are
  deduplicated by `scanLibrary`.

## Non-goals

- Per-job payload inspection beyond the Library target.
- Translating the new copy beyond English (other locales fall back to English).
- Changing scan scheduling or deduplication.
