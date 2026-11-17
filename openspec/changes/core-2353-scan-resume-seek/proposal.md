# Resumed scans seek to their checkpoint; folder probes share the worker budget

Ticket: CORE-2353 (`queue:core:main`).

## Why

A resumed scan walked the whole Library tree from the root and skipped every file at or before its
checkpoint one by one. On a share of spinning disks behind Unraid's shfs, listing and stat'ing a
7201-file TV Library is minutes of work before a run reaches anything new. The run budget now starts
at the first checkpoint (`core-2353-scan-budget-after-resume`), but the walk still held a worker and
the disks for that time.

A folder whose parser reads its siblings (a contextual parser) is also probed file by file before
any of it is imported. Those probes ran one at a time in the ordered consumer, outside the worker
budget, on every scan pass, including for files that had not changed. With several workers they
also ran on top of the fill workers, one probe over the budget.

## What Changes

- A resumed walk does not list or stat a folder whose whole subtree sorts before the checkpoint.
  It lists only the checkpoint's ancestor folders and what follows them, and visits the same files
  after the checkpoint as a full walk does. The empty-root guard counts the skipped folders, so a
  file deleted after the checkpoint is still marked missing.
- The pre-count walk lists folders without stat'ing every entry; it stats only media-named regular
  files.
- A contextual folder's preflight probes run in parallel within the scan's worker budget and come
  back in walk order. With one worker (the default) they still run one at a time.

## Impact

- `internal/scan/scan_walk_state.go`, `internal/scan/scan_walk.go`,
  `internal/scan/scan_walk_parse.go`. No wire, GraphQL, storage or configuration change.
