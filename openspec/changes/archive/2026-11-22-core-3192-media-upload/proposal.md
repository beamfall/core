# Media upload from the manager

Tickets: CORE-3192 (core upload sessions, proposal and commit), CORE-3193 (web Upload page), both
`queue:core:main`. Client follow-ups: CORE-3190 (Apple), CORE-3191 (Android).

## Why

The only way to add media today is to copy files into a library root outside Beamfall and wait for a
scan. An admin with files on a laptop has no way to put them in the right place from the manager,
and no way to see where the server will file them.

## What Changes

- Admin-only REST upload sessions under `/api/media/uploads`. Create states the library, file name,
  size and optional relative folder (from a dropped folder). Bytes arrive as chunked `PUT`s at an
  `Upload-Offset`, are fsynced into a server-owned `0700` staging area under the data dir, and
  resume from the offset the status route reports. Uploads are bounded by a size cap, a free-space
  margin and a cap on open sessions; sessions idle for 24 hours are swept.
- A proposal route returns, per staged file, how the library's plugin parses it, the existing
  Entity it would join (matched by parsed title, year and sequence against the library's
  Entities, with the plugin's display label for the kind), the destination folder under the
  library root, and how a rescan would read the committed path. A target search route lets the
  admin pick another Entity, and the destination folder can be edited directly.
- A commit route places each staged file into its destination folder (creating folders) with the
  existing no-replace, no-partial admission, then enqueues the standard library scan, so the file is
  cataloged by the same pipeline as any other file and keeps its place across rescans. A name
  conflict keeps both files by default; Replace moves the existing file to the audited managed
  trash first. Every commit and replace is recorded in the activity log.
- The web manager gets an Upload page: choose a library, drop files or a folder, watch resumable
  uploads, review and change each proposed assignment, and commit.

## Non-goals

- Pinning an identify candidate at upload time: Fix Match from any item (assign-media) covers it
  after the scan.
- Plugin-declared folder templates. The destination comes from where the matched Entity's files
  already live, or from the parsed title.
- Apple and Android upload screens (CORE-3190, CORE-3191).

## Capabilities

### New Capabilities

- `media-upload`: admin upload of media files into a library root with a reviewed assignment.

## Impact

`internal/store`, `internal/server`, `internal/app`, web manage area. New REST routes; no GraphQL
or wire contract change, no migration.
