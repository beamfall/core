# Scan run budget starts at the run's first checkpoint

Ticket: CORE-2353 (`queue:core:main`).

## Why

A resumed scan walks the whole Library tree again, skipping every file at or before its
checkpoint, before it reaches new work. The two-minute budget counted from the claim, so on a large
Library on a slow share the walk alone spent it: each run yielded at its first checkpoint and moved
forward by one checkpoint's worth of files. The tower's TV scan sat at 7139 to 7143 of 7201 files
through 96 attempts this way; its first run on the current build took 3m19s to reach its first
checkpoint and yielded at it.

## What Changes

- The run budget counts from the run's first checkpoint, so each run gets two minutes of work past
  its resume point. The 100-file batch bound is unchanged.
- A run on such a Library still holds a worker for the walk plus the budget. Making a resumed walk
  start at its checkpoint instead of the root is a separate follow-up.

## Impact

- `internal/scan/scan_job.go`, `internal/scan/scan.go`. No wire, GraphQL or storage change.
