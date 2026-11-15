# Tasks (CORE-1054)

- [x] 1. Scan: `PreviewRoots` (read-only plan) and `LibraryStateDigest`; Go tests
- [x] 2. Graph: `scanPreview` and `commitScanPreview`, in-memory single-use tokens, audit rows,
      error codes; Go tests for preview, valid commit, reused, stale and expired tokens
- [x] 3. `/health` advertises `scan-preview`; activity labels for the two new actions
- [x] 4. Wire contract and management manifest goldens regenerated
- [ ] 5. Publish client-core-wire 2.10.0 and repin consumers; `make gate`; archive after landing
