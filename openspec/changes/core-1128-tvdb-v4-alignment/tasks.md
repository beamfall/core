- [x] Confirm the endpoints against TheTVDB v4 swagger 4.8.0 and the README
- [x] `{year}` substitution, dropped when unknown; identify passes the parsed year and retries
      without it on an empty result
- [x] Multi-condition path predicates and `||` fallbacks for translations
- [x] Scraper `{config.<name>}` falls back to the declared default
- [x] `Engine.LookupByID` over `LookupByIDTemplate`, resolving to the extended record through
      `LookupByIDRecordIDPaths`
- [x] Reference documents with `{ref.<name>:<path>}`, cached a week, failures not cached
- [x] Login token cache lengthened to 27 days
- [x] Recorded-fixture tests: year picks the right same-titled series, configured language with
      original fallback, remote-ID lookup, extra artwork from cached artwork types, artwork types
      unavailable keeps the poster, token reuse and expiry
- [ ] After plugin-sdk v0.3.6 is tagged and pinned: map `ScraperSpec.ReferenceDocuments` and
      `ScraperSpec.LookupByIDRecordIDPaths` in
      `ScraperExtensionsOf`, then repin filmtv to the release carrying the tvdb-series update
