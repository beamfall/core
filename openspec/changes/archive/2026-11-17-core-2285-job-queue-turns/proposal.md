# Give every job a turn and remove finished jobs

Ticket: CORE-2285 (`queue:core:main`).

## Why

On a server with two background workers, a library scan and an asset sweep held both workers for
two days. Each yields and resumes many times, and the queue picked the oldest job by creation
time, so each was picked again the moment it yielded. The daily database backup, the events
compaction, the retention sweep and every poll stayed queued the whole time.

Finished jobs were also never removed, so the Console Job queue grew without bound.

## What Changes

- **Claim order:** within a priority, the job that has waited longest runs first. A yielded job
  has waited since it yielded, not since it was created.
- **Finished jobs:** the daily events compaction removes completed and canceled jobs that
  finished more than 24 hours ago and failed jobs that finished more than 7 days ago, with their
  checkpoint and events.

## Impact

`internal/store`, `internal/app`. No wire-contract, schema or migration change. The `jobs` query
returns fewer finished rows.
