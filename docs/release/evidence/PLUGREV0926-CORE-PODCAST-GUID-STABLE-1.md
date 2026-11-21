# CORE-0201 verification

Base `0bc99a5d334b8811bdfa1d62e3afcdf23f92d504` (origin/main). Go checks ran on wsl-lanes with
`GOWORK=off`.

| Command | Exit | Result |
| --- | --- | --- |
| `go test -count=1 ./internal/scrape ./internal/ingest ./internal/store` | 0 | All three packages passed |
| `golangci-lint run ./internal/scrape/... ./internal/ingest/... ./internal/store/...` | 0 | 0 issues |
| `go vet ./internal/ingest ./internal/store` and `gofmt -l` on changed Go files | 0 | Clean |

- `TestFeedEntriesKeepDerivedGUIDsWhenEnclosuresRotate` covers link precedence, links that
  rotate with enclosures in RSS and Atom, and equivalent named and numeric RSS date zones.
  [RFC 822 §5.1](https://www.rfc-editor.org/rfc/rfc822.html#section-5.1)
- `TestPollDedupesGUIDLessEntryWhenEnclosureRotates` verifies that a rotated enclosure URL
  triggers neither another download nor a second file.
- `TestPollReconcilesLegacyReceiptOnceByEnclosureURL` verifies that a pre-upgrade receipt keyed
  by enclosure URL reconciles on an exact URL or a query-only token rotation, persists the derived
  identity at once, and that the next poll (another rotation) neither downloads nor queues a scan.
- `TestPollTreatsWeakLegacyEvidenceAsNewEntries` verifies that two receipts or two entries on one
  path, a literal "Session-2" title next to "Session", and a title-only match all download as new
  entries, and that a second poll downloads nothing.
- `TestPollDoesNotAliasEnclosureMatchingExplicitGUID` verifies that a new entry downloads when
  its enclosure URL equals another item's explicit GUID, even when the titles match.
- `TestSubscriptionRetentionCountsReassociatedAliasesOnce` verifies that keep-last-one
  retains the most recently associated Media when its receipts have different timestamps.
- `TestPollDedupesByGUIDAcrossPolls` verifies that an explicit GUID still controls deduplication.

Legacy receipts (GUID source `unknown`) reconcile only on an exact enclosure URL, or on the same
scheme, host and path with a different query when exactly one receipt and one feed entry share that
path. Titles, directories, filename suffixes and extensions are never evidence. An unmatched legacy
receipt leaves the entry new: a possible duplicate download is preferred over a skipped episode.
