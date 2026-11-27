## 1. Implementation

- [x] 1.1 Parse `updateFrequency`, `<ttl>`, `sy:updatePeriod`/`sy:updateFrequency`, and HTTP `Cache-Control: max-age` in `internal/scrape`.
- [x] 1.2 Persist adaptive poll state in `server_config` and clean up on subscription move, removal, and library purge.
- [x] 1.3 Compute clamped adaptive poll intervals in `internal/ingest` with `200`/`304`, rolling feed, and upgrade carryover parity.

## 2. Verification

- [x] 2.1 Add unit tests and 500-feed simulation in `internal/scrape` and `internal/ingest`, run focused checks, and validate OpenSpec.
