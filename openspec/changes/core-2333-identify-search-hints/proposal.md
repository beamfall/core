# Identification carries parser search hints into scraper queries

Ticket: CORE-2333 (`queue:core:main`).

## Why

Identification searches a scraper with the parsed title (and year) only. For items whose title is
not unique, such as a short track title shared by many recordings, the search returns many
unrelated candidates and the scorer cannot pick the right one. The plugin already knows more about
the file (its parser and the file's embedded tags), but has no way to put it in the query.
plugin-sdk v0.4.8 adds named search hints: `ParsedMedia.Hints`, `Bundle.EmbeddedTagHints`,
`ScraperSpec.RequiredSearchHints` and the `{hint.<key>}` SearchTemplate placeholder.

## What Changes

- Core repins `github.com/beamfall/plugin-sdk` to v0.4.8.
- Scan fills the item's hints from its parser, then fills hints the parser left empty from the
  file's non-empty embedded tags named by the bundle's `EmbeddedTagHints` (the parser's value
  wins). A hint value that does not fit the SDK limits is dropped; a hint set that fails SDK
  validation is dropped whole.
- Automatic identification passes the hints to the scraper search. The scrape engine substitutes
  each `{hint.<key>}` placeholder in a SearchTemplate's URL query with the hint value, escaped with
  `url.QueryEscape`. A missing or empty hint substitutes the empty string. Placeholders in the URL
  fragment are left alone.
- A scraper whose `RequiredSearchHints` the item does not all supply is skipped for that search:
  automatic identification picks the next searchable scraper for the item's class, and a direct
  search returns no candidates without sending a request.
- Manual Search and Search-with-year take the hints the parser derives from the primary file's
  path (embedded tags are not stored, so only parser hints are available there).

## Impact

- Code: `internal/scrape` (hint substitution, required-hint check), `internal/scan` (hint merge),
  `internal/identify` (hints in search and scraper choice, optional `MediaHintSource`),
  `internal/app` (hint wiring and adapters), `go.mod`.
- No store method, migration, GraphQL or wire-contract change. Core keeps no domain vocabulary:
  hint keys and tag names come from plugin bundles.
