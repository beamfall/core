# Media versions: a rescan merges an already-split copy into its title's Media

Ticket: CORE-2490 (`queue:core:main`). Follows CORE-2473.

## Why

CORE-2473 stopped new scans from splitting two unlabelled copies of one film into two Media, but a
library that an earlier scan had already split keeps the split: every rescan refuses the attach
because the second copy's bytes belong to its own Media. On the tower, `Disclosure Day (2026)` is
still listed twice and its Version picker never appears.

## What Changes

- **Scan**: when the scan reads a directory's cataloged files and finds that the files of one title
  (same parsed class, title, year, sequence and index, none with a parsed label) sit on more than one
  Media, it asks the store to merge them before it walks the directory's files.
- **Store** (`MergeSplitVersions`): in one catalog transaction it keeps the Media with the most
  files (the earliest imported on a tie) and, for each other Media whose single live file is
  unlabelled, moves that file onto the survivor under a derived label (the CORE-2473 rule: the
  resolution class, then the container; the survivor's lone unlabelled file takes its own), copies
  Collection membership and tags the survivor lacks, and moves the merged Media to the trash. A
  Media the rule cannot place stays as it is. The batch is journaled as `merge-version`, so
  `UndoBatch` restores the split: the file goes back to its Media unlabelled, the survivor's labels
  and Media-row facts return, the copied Collection and tag rows go, and the merged Media leaves the
  trash. A Media whose merge was undone is never merged again by a scan.
- **User state**: the merged Media's playback progress, ratings, favourites and watchlist entries
  are copied to the survivor where the survivor has none (the survivor's own values win), and its
  queue entries are repointed to the survivor where the survivor is not already queued. Progress is
  copied through a playback projection event, so the projection can still be rebuilt from the log.
  As with entity merges (CORE-2459), the copies stay after an undo and the merged Media keeps its
  own rows, so an undo loses nothing; queue entries move back.
- `media.deleted` is emitted for each merged Media and `media.updated` for the survivor, and a
  `media.merge` activity entry names the batch. Undo emits `media.created` and `media.updated`.
- Encrypted libraries are skipped, and so is every merge while trash is disabled, as for other
  autonomous trash paths.

## Not in this change

Per-Media records that are derived, transient or access-bearing stay with the merged Media: access
grants and requests (copying would widen access), metadata and artwork rows (the survivor has its
own), analysis rows (markers, detections, renditions; re-derived from the file), offline packs,
stream tokens and handoff offers, mutes, and audit logs.

## Impact

- `internal/store` (no migration: `op_batch.kind` is free text), `internal/scan/scan_walk_versions.go`.
- No wire change.

## Rollback

Revert the change. Merges already made stay undoable through their `merge-version` batch only while
the revert is not deployed; after a revert, `UndoBatch` refuses the unknown kind.
