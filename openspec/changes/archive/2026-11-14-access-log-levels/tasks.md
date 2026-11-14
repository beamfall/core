# Tasks (CORE-1159)

- [x] 1. Record when the response starts in the tracked response writer
- [x] 2. Choose the access line's level from status and time to first byte; skip formatting when
      the level is disabled; add `first_byte_ms`
- [x] 3. Tests for routine, 5xx, slow, long-stream and abandoned requests; update
      `docs/security/runbooks/logs.md`
- [x] 4. `make gate` at landing; `openspec archive access-log-levels`
