## ADDED Requirements

### Requirement: Search passes the year when it is known

A scraper search template MAY carry `{year}`. The engine SHALL substitute the parsed year when it is
known and SHALL drop the query parameter that carries it when it is not. Automatic identification
SHALL pass the parsed year and SHALL retry once without it when the year-filtered search finds no
candidates.

#### Scenario: Two same-titled series

- **WHEN** a file parses to a title and year, and the provider has two series with that title from
  different years
- **THEN** the search carries the year and the series from that year is matched

#### Scenario: Unknown year

- **WHEN** the parsed year is unknown
- **THEN** the search request carries no year parameter

### Requirement: Fields can map from a configured language with fallback

Scraper paths SHALL support predicates with several conditions and ordered fallbacks, so a field
can read a translation selected by plugin configuration and fall back to another path when it is
absent. A configuration placeholder SHALL resolve to the field's declared default when the owner
has saved no value.

#### Scenario: Translation present

- **WHEN** the configured language has a translation
- **THEN** the title and summary come from that translation

#### Scenario: Translation absent

- **WHEN** the configured language has no translation
- **THEN** the title and summary come from the record's original-language values

### Requirement: Static lookup documents map provider ids

A scraper MAY declare reference documents. The engine SHALL fetch each with the scraper's
authorization, cache it per URL for its declared hours or one week by default, not cache a failed
fetch, and resolve `{ref.<name>:<path>}` placeholders in field and artwork paths from it.

#### Scenario: Extra artwork types

- **WHEN** a record lists artworks by numeric type and the scraper maps background, banner and
  clearlogo through the artwork-types document
- **THEN** those artworks are mapped, and the document is fetched once within its cache window

#### Scenario: Reference document unavailable

- **WHEN** the reference document cannot be fetched
- **THEN** paths that use it map nothing and every other field still maps

### Requirement: Login tokens are reused for most of their lifetime

The engine SHALL reuse a login-bearer token for 27 days and SHALL log in again after that, after a
401, or when a credential the login used is invalidated.

#### Scenario: Token reuse

- **WHEN** a second scrape runs a week after the first
- **THEN** no new login request is sent

### Requirement: External-id lookups resolve to the full record

When a scraper declares an id-lookup record path, an external-id lookup SHALL read only the
scraper's own record id from the `LookupByIDTemplate` document and SHALL map the `LookupTemplate`
document of that id. An external id that resolves to no record SHALL be an error.

#### Scenario: IMDb id on TheTVDB

- **WHEN** an IMDb id is looked up and `/search/remoteid/{id}` returns series 700303
- **THEN** the engine fetches `/series/700303/extended?meta=translations` and maps its translated
  title, summary and extra artwork

#### Scenario: Unknown external id

- **WHEN** `/search/remoteid/{id}` returns no records
- **THEN** the lookup fails rather than mapping an empty document
