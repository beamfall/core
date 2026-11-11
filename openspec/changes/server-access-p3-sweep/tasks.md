## 1. Server

- [x] 1.1 Drop the artwork and entity-artwork routes from the token-byte cross-origin list
- [x] 1.2 Open offline-pack content beneath the pack root with a regular-file and same-file check
- [x] 1.3 Return the queued job IDs in the `/v1/scans` enqueue-failure body; document it in OpenAPI
- [x] 1.4 Tests: artwork cross-origin refusal, pack member and directory symlinks, partial scan

## 2. Verify and close

- [x] 2.1 Run `go test ./internal/server/ ./internal/offlinepack/`, with `-race` on offlinepack and the changed server tests
- [ ] 2.2 Run `make gate`
- [ ] 2.3 Run `openspec archive server-access-p3-sweep` after landing, before completing CORE-0735
