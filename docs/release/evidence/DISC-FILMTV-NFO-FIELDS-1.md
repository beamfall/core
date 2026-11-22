# DISC-FILMTV-NFO-FIELDS-1 (CORE-0361) evidence

Kodi-family NFO sidecars beside a film or an episode file (`movie.nfo`, `<stem>.nfo`,
multi-root files) now hydrate the Media at a new `local` metadata tier. That tier ranks
above operator and scan writes and below manual edits, and its fields read as locked.
Core stays generic: a mapper may implement `HydrationSidecarMapper`, which signs a path's
sidecar without reading it and maps it to a local part (built-in values, declared Media
fields, Media relations, the relations the format owns, the signature of the parsed bytes,
or an unreadable flag). The store applies a local part in one transaction on the Media's
own catalog plane (`ApplyLocalMetadata`) and releases a removed sidecar the same way
(`ReleaseLocalMetadata`). Sidecar relation rows carry the CORE-3152 writer `local`.

- Base: `e9dd7e3d4` (stack CORE-3111/3152) plus OpenSpec `core-0361-local-sidecar-tier`
  and this change, through review round 1 (OpenSpec `50714d16d`)
- Plugin: beamfall-plugin-filmtv `17156b2` (branch `cl/CORE-0361-nfo-import`, on `fa572ba`).
  Core needs a filmtv tag from that commit (v0.1.28); the `go.mod` pin moves at landing.
- Run: 2026-11-21, go1.27.4 darwin/arm64

| Command | rc | Result |
|---|---|---|
| `GOWORK=off go test -count=1 ./internal/identify/ ./internal/app/ ./internal/store/ ./internal/scan/` | 0 | ok |
| `GOWORK=off go test -count=1 -run TestVocab ./internal/conformance/` | 0 | ok |
| `go test -count=1 ./cmd/beamfall/...` (uncommitted go.work with the filmtv worktree) | 0 | ok |
| `gofmt -l internal cmd/beamfall` | 0 | no output |
| `golangci-lint run ./internal/store/... ./internal/app/... ./internal/scan/... ./cmd/beamfall/...` | 0 | 0 issues |
| `openspec validate --all --strict` | 0 | 217 passed |
| filmtv `go test ./...`, gofmt, golangci-lint | 0 | ok, 0 issues |

## Mutation checks

Each mutant below was applied alone and made the named test fail.

| Mutant | Killed by |
|---|---|
| Local relation DELETE drops its `writer = 'local'` filter | `TestApplyLocalMetadataOwnsOnlyLocalRelationRows` |
| Owned relations skipped when the parse lists no rows | `TestApplyLocalMetadataOwnsOnlyLocalRelationRows`, `TestScanWritesSidecarValuesAtLocalTier` |
| Commit between the release and the new writes | `TestApplyLocalMetadataFailureKeepsPreviousState` |
| Write through the shared catalog instead of the Media's plane | `TestEncryptedApplyLocalMetadataStaysInProtectedPlane` |
| An unreadable sidecar treated as removed | `TestSidecarUnreadableIsNotRemoval` |
| Record the stat signature instead of the parsed one | `TestSidecarRecordsParsedSignature` |
| Scan fast path skips the sidecar refresh | `TestScanRefreshesSidecarsWithoutAProbe` |
| Scraper upsert may take a `local` row over (entity.go guard removed) | `TestApplyLocalMetadataOwnsOnlyLocalRelationRows` |
| Field-less Entity written with an upsert instead of an ensure | `TestHydrationFieldlessEntityKeepsStoredFields` |
| Change check compares the bare signature, not the source-qualified identity | `TestSidecarIdentityNamesTheSource` |
| Record the bare signature | `TestSidecarIdentityNamesTheSource`, `TestSidecarRecordsParsedSignature` |
| A failed lookup moves on to the next candidate | `TestSidecarLookupFailureIsUnreadable` |
| A failed lookup applies instead of keeping the previous values | `TestSidecarLookupFailureIsUnreadable` |
| Release leaves the identified marks set | `TestReleaseClearsIdentifiedMarks` |
| A sidecar listing a released row again does not reclaim it | `TestRestoredSidecarReclaimsReleasedRows` |
| A URL-only sidecar is skipped instead of applied empty | `TestFilmTVURLOnlySidecarIsAnEmptyOwnedParse` |
| Grouped parts skip the sidecar refresh | `TestScanRefreshesGroupedPartSidecars` |

## Acceptance

| Item | Test |
|---|---|
| Golden fixtures (film, folder film, episode, multi-root, Latin-1, truncated, none) produce the expected fields, all declared in the bundle | filmtv `TestLocalNFOHydrationGolden`, `TestLocalNFOHydrationFieldsAreDeclared`, `TestFilmTVHydrationCarriesFilmSidecar` |
| A later identify/TMDB write changes no local value; a manual edit outranks it | `TestScanWritesSidecarValuesAtLocalTier`, `TestLocalTierLocksUntilReleased` |
| An NFO edit re-extracts on an unchanged media file, without a probe, beside any version | `TestScanRefreshesSidecarsWithoutAProbe`, filmtv `TestNFOSignatureTracksSidecars`, `TestNFOSignatureNamesTheSidecar` |
| Removing the NFO unlocks the fields but keeps their values | `TestScanWritesSidecarValuesAtLocalTier`, `TestLocalTierLocksUntilReleased`, `TestScanRefreshesSidecarsWithoutAProbe` |
| A sidecar edit replaces only local rows; manual and other-writer rows stay; an emptied relation is emptied | `TestApplyLocalMetadataOwnsOnlyLocalRelationRows`, `TestApplyLocalMetadataDropsFieldsTheSidecarNoLongerStates` |
| A failed apply keeps the previous values, locks and signature | `TestApplyLocalMetadataFailureKeepsPreviousState` |
| Encrypted libraries take the values in their own plane | `TestEncryptedApplyLocalMetadataStaysInProtectedPlane` |
| Unreadable is not removal | `TestSidecarUnreadableIsNotRemoval`, `TestFilmTVSidecarUnreadableIsNotRemoval`, filmtv `TestLocalNFOHydrationUnreadableIsNotRemoval`, `TestLocalNFOHydrationSyntaxErrorIsUnreadable`, `TestLocalNFOHydrationRefusesAnEditDuringTheRead` |
| The signature is the parsed bytes' and ignores field locks | `TestSidecarRecordsParsedSignature`, `TestApplyLocalMetadataSignatureIgnoresLocks` |
| Container sidecars are not imported | `TestFilmTVHydrationIgnoresContainerSidecars` |
| Watched import honours the selected categories and the importing profile; a folder `movie.nfo` describes only the main feature | `TestFilmTVNFOImporterHonoursCategories`, `TestFilmTVNFOImportMarksWatchedForTheImportingProfileOnly`, filmtv `TestNFOSourceHonoursCategorySelection`, `TestNFOSourceCarriesPlayRecord`, `TestFolderFilmNFODescribesOnlyTheMainFeature` |
| Certification uses the US entry; a foreign-only rating never sets the ceiling | filmtv `TestSelectCertification`, `TestForeignRatingNeverSetsTheCeiling` |
| The SQL rank table matches the Go precedence | `TestSourceRankValuesMatchGo` |
| Local values outside the bundle are rejected; an undeclared signature field errors | `TestValidateLocalRejectsOutOfBundleValues`, `TestSidecarHydrationNeedsDeclaredSupport` |
| A field-less Entity keeps stored fields | `TestHydrationFieldlessEntityKeepsStoredFields` |
| A failed sidecar lookup is unreadable and stops the selection; only a clean not-found is absent | `TestSidecarLookupFailureIsUnreadable`, `TestFilmTVSidecarLookupFailureIsAnError`, filmtv lookup tests |
| The recorded identity names the selected sidecar's source | `TestSidecarIdentityNamesTheSource` |
| Release clears the identified marks, so a rescan takes the file's own title | `TestReleaseClearsIdentifiedMarks` |
| A restored sidecar reclaims its released credits | `TestRestoredSidecarReclaimsReleasedRows` |
| A sidecar with no local values (URL-only) applies as an empty owned plan | `TestSidecarWithNoValuesReplacesTheLastOne`, `TestFilmTVURLOnlySidecarIsAnEmptyOwnedParse` |
| An edited sidecar beside grouped parts is refreshed | `TestScanRefreshesGroupedPartSidecars` |

## Known gaps

- Show and season NFO import is cut: CORE-3165.
- `playcount` collapses to watched; the SDK WatchState has no count: CORE-3166.
- Actor thumbnails are not imported: CORE-3167.
- `ReplaceMediaRelations` and single-cardinality `UpsertEntityRelation` ignore the writer, so an
  enrichment replace can drop local rows until the sidecar changes: CORE-3168.
- An encrypted library's plane has no sort title, so a local sort title is skipped there: CORE-1868.
- user-rating is stored per Media, not per profile.
- An unchanged file costs one sidecar stat plus one field read per rescan.
