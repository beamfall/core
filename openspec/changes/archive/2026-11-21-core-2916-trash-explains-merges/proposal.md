# Trash explains verified-copy merges

Ticket: CORE-2916 (`queue:core:main`).

## Why

On 2026-11-20 the scan's verified-copy merge (CORE-2762) folded byte-identical duplicate files into
one Media as an extra Version. Each folded Media went to Trash: on one server 198 merge batches
left 701 Trash rows that look like deletions. No files were lost, but Trash gives no reason, the
`media.merge` activity entry has no actor (the log reads "someone"), and nothing shows the operator
which survivors now hold two identical copies on disk.

## What Changes

- `trashList` entries trashed by a merge batch that has not been undone carry `mergedInto`: the
  batch id, the survivor's id and title, and the batch time.
- Manage › Trash lists those entries apart from real deletions, one group per batch, each saying
  "Merged into <survivor> as a Version (automatic, scan)" with a link to the survivor and an
  Undo merge action that calls the existing `undoBatch`. Per-row Restore stays for deletions only.
- `activityLog` and `activityEntry` expose `systemActor`, derived from the closed action vocabulary
  like `reversible` and not stored: a `media.merge` entry with no session, Profile, token or key is
  the scan's, so it reads `scan`. No migration, and entries already written read correctly. The
  activity sentence names the library scan.
- A manager query `duplicateCopies` reports Media whose live Versions share an OSHash and size,
  with each copy's path and size. The web shows it read-only. The server never deletes files.
- A store test pins that purging a merge-trashed Media leaves the moved files on the survivor.

## Non-goals

- Changing when or how the scan merges copies.
- Deleting, moving or renaming files on disk.
- Storing a system actor column (a derived field covers the one server-written action today).
