# Server logs in the console

Ticket: CORE-1716 (`queue:core:main`).

## Why

To see why something failed, an owner had to reach the host and read the server's stderr. Support
threads need the recent log too, and a raw log carries tokens, secrets, IP addresses and the user
names in home folder paths.

## What Changes

- The operator logger tees every line into a bounded in-memory tail (2,000 lines, 8 KiB per line)
  beside the stderr text handler. Each line is redacted before it is kept; stderr is unchanged.
- Redaction lives in a new `internal/redact` package. It reuses the job-error secret patterns
  (moved out of `internal/jobs`, which now calls it) and adds secret-named attribute values,
  home-directory user names and IPv4 and IPv6 addresses.
- A new Admin-Session gated GraphQL query `serverLogs(limit)` returns the kept lines oldest first,
  with `available` false when the server keeps no tail.
- The console gains a Server logs page under Monitor, next to the Activity log and Library Health.
  It shows the lines and downloads them as a plain-text bundle with a short header.

## Impact

- Wire contract: one new top-level query field, `serverLogs`. Goldens regenerated; the published
  versioned artifact is left to the batch release.
- No storage, schema or migration change. Nothing is written to disk.
