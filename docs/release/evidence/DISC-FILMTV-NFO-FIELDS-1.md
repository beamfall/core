# DISC-FILMTV-NFO-FIELDS-1 (CORE-0361) evidence

Kodi-family NFO sidecars (`movie.nfo`, `<stem>.nfo`, `tvshow.nfo`, `season.nfo`,
multi-root and multi-episode files) now hydrate Media at a new `local` metadata
tier. That tier ranks above operator and scan writes and below manual edits, and
its fields read as locked. Core stays generic: the hydration seam gains an
optional local part (`HydrationPlan.Local`) and an optional sidecar-signature
mapper (`HydrationSidecarMapper`). The plugin parses the NFO and supplies values
and the signature (each sidecar's size and mtime).

- Base: `ec99681ca` plus OpenSpec `core-0361-local-sidecar-tier` (`8979352cf`) and this change
- Plugin: beamfall-plugin-filmtv `4fa96ca` (branch `cl/CORE-0361-nfo-import`). Core
  needs a filmtv tag from that commit; the `go.mod` pin moves up from v0.1.25.
- Run: 2026-11-21, go1.27.4 darwin/arm64

| Command | rc | Result |
|---|---|---|
| `GOWORK=off go test -count=1 ./internal/identify/... ./internal/app/... ./internal/store/... ./internal/scan/... ./internal/migrate/... ./internal/conformance/...` | 0 | ok |
| `go test -count=1 ./cmd/beamfall/...` (uncommitted go.work with the filmtv worktree) | 0 | ok |
| `gofmt -l internal cmd/beamfall` | 0 | no output |
| `golangci-lint run` on the changed packages | 0 | 0 issues |
| `openspec validate --all --strict` | 0 | 203 passed |
| filmtv `go test ./...`, gofmt, golangci-lint | 0 | ok, 0 issues |

When the scan's sidecar re-hydration branch is mutated out,
`TestScanWritesSidecarValuesAtLocalTier` fails. That test pins the refresh.

## Acceptance

| Item | Test |
|---|---|
| Golden fixtures (film, folder film, episode, multi-episode, show-only, Latin-1, truncated, none) produce the expected fields, and every field is declared in the bundle | filmtv `TestLocalNFOHydrationGolden`, `TestLocalNFOHydrationFieldsAreDeclared`, `TestFilmTVHydrationCarriesFilmSidecar`, `TestFilmTVHydrationCarriesContainerSidecars`, `internal/mediaimport` `TestParseNFODocument*` |
| A later identify/TMDB write changes no local value | `TestScanWritesSidecarValuesAtLocalTier`, `TestLocalTierLocksUntilReleased` |
| An NFO edit (size or mtime) re-extracts on an unchanged media file | `TestScanWritesSidecarValuesAtLocalTier`, filmtv `TestNFOSignatureTracksSidecars` |
| Removing the NFO unlocks the fields but keeps their values | `TestScanWritesSidecarValuesAtLocalTier`, `TestLocalTierLocksUntilReleased` |
| `playcount=1` marks the item watched for the importing profile only | `TestFilmTVNFOImportMarksWatchedForTheImportingProfileOnly` (cmd/beamfall), filmtv `TestNFOSourceCarriesPlayRecord` |
| The SQL rank table matches the Go precedence | `TestSourceRankValuesMatchGo` |
| Local values outside the bundle are rejected, and an undeclared signature field errors | `TestValidateLocalRejectsOutOfBundleValues`, `TestSidecarHydrationNeedsDeclaredSupport` |
| A container Entity write keeps stored fields | `TestHydrationEntityWritesKeepStoredFields` |

## Known gaps

- Container (season/show) Entity fields carry no tier, so a later identify Entity upsert can replace NFO values.
- Local relations replace same-name relations, which drops scraper rows of that name until the next identify.
- user-rating is stored per Media, not per profile. Actor thumbs are not imported.
- An unchanged file costs one sidecar stat plus one field read per rescan.
