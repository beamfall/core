# CORE-0201 verification

Base `0bc99a5d334b8811bdfa1d62e3afcdf23f92d504` (origin/main). Go checks ran with
`GOWORK=off` and `GOCACHE=/tmp/codex-gocache`.

| Command | Exit | Result |
| --- | --- | --- |
| `go test -count=1 ./internal/scrape/... ./internal/ingest/...` | 0 | Both packages passed on the final diff |
| `go test -count=1 ./internal/scrape ./internal/ingest ./internal/store` | 0 | All three packages passed after adding the receipt alias |
| `go vet ./internal/scrape ./internal/ingest ./internal/store` | 0 | Clean |
| `golangci-lint run ./internal/scrape ./internal/ingest ./internal/store` | 0 | 0 issues |
| `gofmt -l` on the five changed Go files | 0 | No output |

- `TestFeedEntriesKeepDerivedGUIDsWhenEnclosuresRotate` covers link precedence and stable
  title and publication date identities in RSS and Atom.
- `TestPollDedupesGUIDLessEntryWhenEnclosureRotates` verifies that a rotated enclosure URL
  triggers neither another download nor a second file.
- `TestPollPreservesLegacyEnclosureIdentityAcrossRestartAndRotation` verifies that the poll
  matches the existing receipt, then reopens the catalog and confirms the derived identity
  still points to the existing Media after rotation.
- `TestPollDedupesByGUIDAcrossPolls` verifies that an explicit GUID still controls deduplication.

Both new ingest tests reproduced the duplicate download on the baseline (exit 1).
Legacy matching requires a feed response that still contains the stored enclosure URL.
