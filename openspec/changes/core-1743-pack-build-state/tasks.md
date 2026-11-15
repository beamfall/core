## 1. Store

- [x] 1.1 Migration 0116 `offline_pack_build` with closed state and progress checks; schema-freeze audit
- [x] 1.2 Track, progress, fail, restart, dismiss and list; sealing deletes the row; privacy delete covers it
- [x] 1.3 Store tests for the lifecycle, cross-Profile isolation and constraints

## 2. Offline pack service

- [x] 2.1 Track the build at job start, record step progress, map terminal errors to failure codes
- [x] 2.2 `RetryBuild` re-snapshots the payload for the caller and restarts a failed row once

## 3. Graph

- [x] 3.1 `OfflinePackInfo.progress` and `failureReason`; `offlinePacks` lists builds first
- [x] 3.2 `retryOfflinePack` mutation; `revokeOfflinePack` dismisses failed builds
- [x] 3.3 Graph tests for building, ready, failed and retry; wire goldens regenerated
