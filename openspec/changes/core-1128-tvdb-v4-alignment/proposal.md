# Align the TheTVDB scraper with the v4 API

Ticket: CORE-1128 (`queue:core:main`).

## Why

The owner asked for TheTVDB to be used as its v4 API documents. Every endpoint below was confirmed
against the official v4 swagger (`docs/swagger.yml` in `thetvdb/v4-api`, version 4.8.0, sha256
`ff4a162b2b9192f83abdda672fa64c4f88fe5c1413c6c9eccc7b46a752c633f3`) and the repository README:

| Need | Endpoint (swagger 4.8.0) |
|---|---|
| Login | `POST /login {apikey, pin}`; token at `data.token`, valid one month |
| Search | `GET /search?query=&type=series&year=` (`year` filters series and movies) |
| Record with translations | `GET /series/{id}/extended?meta=translations` (`translations.nameTranslations[]`, `translations.overviewTranslations[]`, `originalLanguage`, `artworks[]` with numeric `type`) |
| Remote IDs | `GET /search/remoteid/{remoteId}` → `data[].series` |
| Artwork types | `GET /artwork/types` → `data[] {id, slug, recordType}` |
| Season types | `GET /series/{id}/episodes/{season-type}` |

The engine gaps were: no `{year}` substitution, a one-hour token cache, no way to map a field
through a static lookup document, and no multi-condition or fallback path syntax for translations.

## What Changes

- **Year.** `scrape.Variables.Year` fills `{year}` in search templates. When the year is unknown
  the query parameter that carries it is dropped rather than sent empty. `AutoIdentify` passes the
  parsed year and, when a year-filtered search finds nothing, retries once without it.
- **Language.** Paths accept multi-condition predicates (`list[a=b,c=d].field`) and ordered
  fallbacks (`p1||p2`), and `{config.<field>}` already reads plugin config, so a plugin maps the
  title to the translation in its configured language and falls back to the original-language
  base value. `{config.<name>}` now falls back to the field's declared default when the owner has
  saved no value (`internal/app/scrape_config.go`), matching what the Console shows, so the
  plugin's default language applies before anyone edits it.
- **Remote IDs.** `Engine.LookupByID` runs a scraper's `LookupByIDTemplate` for an external ID
  scheme; the plugin maps IMDb and TMDB to `/search/remoteid/{id}`.
- **Artwork.** A scraper may declare `ReferenceDocuments` (plugin-sdk v0.3.6): static documents
  fetched with its authorization and read through `{ref.<name>:<path>}` placeholders in field and
  artwork paths. The artwork-type ids for background, banner and clearlogo come from
  `/artwork/types` at runtime and are never hard-coded. A failed reference fetch is not cached and
  leaves the affected paths empty; other fields (the base-record poster) still map. Core reads the
  declaration through `plugin.ScraperExtensionsOf` until the repin.
- **Caching.** Login tokens are cached for 27 days (TheTVDB tokens last one month; the margin keeps
  a token from expiring mid-scrape, and a 401 still forces a fresh login). Reference documents are
  cached per URL for one week unless the scraper declares `CacheHours`. Identification results are
  already persisted: a rescan re-identifies only items with no curated fields that are not in the
  Unmatched queue (`internal/scan/scan_walk_import.go`, `retryAutoIdentify`), so it does not
  re-query matched items. A refresh path through `/updates` stays a later ticket.
- **Episode stills.** No client crop assumption is introduced; artwork aspect handling is
  unchanged.

## Overlaps (resolved, not duplicated)

- **CORE-0363** (non-English metadata): the TheTVDB language leg is done here (configured language
  with original-language fallback). Other providers stay with CORE-0363.
- **CORE-0360** (NFO and filename ID hints): the TheTVDB remote-ID templates and the engine's
  `LookupByID` are done here. Wiring parsed NFO or filename IDs into identification stays with
  CORE-0360; core does not call `LookupByID` from identify yet.
- **CORE-0362** (multi-episode and season handling): episode lookup by season type
  (`/series/{id}/episodes/{season-type}`) stays with CORE-0362, which owns episode records.
- **CORE-1127 / CORE-1129**: the PIN login is CORE-1129's; credential presentation is CORE-1127's.

## Impact

- Domain-agnostic: core gains generic engine features; every TheTVDB detail lives in the filmtv
  plugin and in test fixtures.
- Tests use recorded, invented fixtures (`internal/scrape/testdata/tvdb-v4/`); no live calls and no
  real key.
- No migration, no new Store method, no wire change.
