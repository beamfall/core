## 1. Scan

- [x] 1.1 `scan.MediaTitler` optional extension applied in both import paths
- [x] 1.2 Failing tests first: per-file title replaces the parsed title; empty keeps it

## 2. Film & TV wiring

- [x] 2.1 Pin plugin-filmtv v0.1.7 (with plugin-sdk v0.3.4 and every bundled plugin on a release that declares it)
- [x] 2.2 Film & TV hydration mapper: season Entity scoped by series, `season-episode` in episode order, `season-of` to the show, completion flag
- [x] 2.3 Tests: grouping plan validates against the bundle; movies marked complete; hooks registered
- [x] 2.4 One-time startup backfill scan for Libraries holding Media without the marker, recorded after the enqueue

## 3. Verify and close

- [ ] 3.1 Focused package tests and `internal/conformance`
- [ ] 3.2 Run `make gate`
- [ ] 3.3 Run `openspec archive local-media-containers` after landing, before completing CORE-1411
