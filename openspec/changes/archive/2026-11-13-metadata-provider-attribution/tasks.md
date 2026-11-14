## 1. Provenance

- [x] 1.1 Catalog migration `0112_media_metadata_provenance.sql` and the schema-freeze bump
- [x] 1.2 Store upsert and read of a Media item's metadata provenance
- [x] 1.3 `identify.Service.Apply` records provenance; the composition root wires the store

## 2. Wire

- [x] 2.1 `media(id)` resolves `metadataAttribution` from the active bundle
- [x] 2.2 `PluginScraperInfo.attribution` and the deduplicated `metadataCredits` read
- [x] 2.3 Wire goldens regenerated through their documented commands

## 3. Web

- [x] 3.1 Player Media info panel renders the attribution link
- [x] 3.2 Profile settings Credits section lists `metadataCredits`

## 4. Tests

- [x] 4.1 Failing tests first: provenance recorded and overwritten, attribution resolution
  (inactive plugin, no attribution), credits dedupe, web rendering

## 5. Verify and close

- [ ] 5.1 Focused package tests, `internal/conformance`, web lint, typecheck and tests, `make gate-preflight`
  (open: `TestWireContractPublishedArtifactFreshness` needs the next wire-contract artifact published)
- [ ] 5.2 Run `make gate`
- [ ] 5.3 Run `openspec archive metadata-provider-attribution` after landing, before completing CORE-1126
