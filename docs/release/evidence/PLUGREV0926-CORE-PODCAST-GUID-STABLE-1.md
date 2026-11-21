# CORE-0201 verification

Base `0bc99a5d334b8811bdfa1d62e3afcdf23f92d504` (origin/main). Go checks ran with
`GOWORK=off` and `GOCACHE=/tmp/codex-gocache`.

| Command | Exit | Result |
| --- | --- | --- |
| `go test -count=1 ./internal/scrape ./internal/ingest ./internal/store` | 0 | All three packages passed |
| `go vet ./internal/scrape ./internal/ingest ./internal/store` | 0 | Clean |
| `golangci-lint run ./internal/scrape ./internal/ingest ./internal/store` | 0 | 0 issues |
| `gofmt -l` on the seven changed Go files | 0 | No output |

- `TestFeedEntriesKeepDerivedGUIDsWhenEnclosuresRotate` covers link precedence, links that
  rotate with enclosures in RSS and Atom, and equivalent named and numeric RSS date zones.
  [RFC 822 §5.1](https://www.rfc-editor.org/rfc/rfc822.html#section-5.1)
- `TestPollDedupesGUIDLessEntryWhenEnclosureRotates` verifies that a rotated enclosure URL
  triggers neither another download nor a second file.
- `TestPollPreservesLegacyEnclosureIdentityAcrossRestartAndRotation` verifies that the first
  upgraded poll matches the existing Media after the enclosure has already rotated. It reopens
  the catalog and polls after another rotation to verify that the alias persists.
- `TestPollDoesNotAliasEnclosureMatchingExplicitGUID` verifies that a new entry downloads when
  its enclosure URL equals another item's explicit GUID, even when the titles match.
- `TestSubscriptionRetentionCountsReassociatedAliasesOnce` verifies that keep-last-one
  retains the most recently associated Media when its receipts have different timestamps.
- `TestPollDedupesByGUIDAcrossPolls` verifies that an explicit GUID still controls deduplication.

The repair regressions reproduced the review findings on the earlier implementation (exit 1).
Legacy reconciliation uses unambiguous title evidence from Media in the shared catalog.
Ambiguous receipts and encrypted catalogs are excluded from reconciliation.
