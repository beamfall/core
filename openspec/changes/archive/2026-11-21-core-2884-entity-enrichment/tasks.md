## Implementation

- [x] Add migration 0128 `entity_enrichment`, the schema freeze count and the regenerated backup fixture.
- [x] Add store reads and writes for entities due for enrichment and their recorded checks.
- [x] Add `identify.EntityEnrichment`, its job and handler, and `scrape.IsTransient`.
- [x] Validate enrichment hooks in the app, register the job and enqueue it at boot and after scans.
- [x] Wire the music plugin's album enrichment in `cmd/beamfall`.
- [ ] Verify with store, handler and hook-validation tests, then on a real library.
