## ADDED Requirements

### Requirement: Feed directory caching and upstream budget

The server SHALL cache successful feed directory results for ten minutes in a bounded per-server cache. Text keys SHALL normalize case and whitespace. URL keys SHALL preserve case-sensitive paths and queries.

#### Scenario: Repeated text search

- **WHEN** searches differ only in case or whitespace within ten minutes
- **THEN** later searches receive the cached results without an upstream request

#### Scenario: Upstream budget exhausted

- **WHEN** uncached searches would exceed 15 upstream requests in a minute
- **THEN** the server serves any matching cache entry and refuses further required upstream requests immediately with Graph `THROTTLED`
- **AND** optional enrichment may be skipped when the primary results are already available

### Requirement: Feed directory identity and credentials

The server SHALL send `Beamfall/<version> (+https://beamfall.com/bot)` as the User-Agent on directory requests and feed polls, using the running release version. The optional extended directory SHALL receive requests only when the operator has supplied both its API key and secret.

#### Scenario: Extended directory credentials absent

- **WHEN** the operator has not configured a key
- **THEN** the server uses the primary directory and makes zero requests to the extended directory

### Requirement: Feed discovery from URLs

The existing `searchFeeds` field SHALL accept Apple Podcasts show links and absolute website URLs. Apple show links are resolved by ID through the iTunes lookup API. Website discovery reads RSS alternate links and resolves relative links against the fetched page. Outbound discovery requests SHALL use the hardened fetcher, bounded response bodies, and the shared upstream budget.

#### Scenario: Pasted show link

- **WHEN** a user searches for an Apple show URL with a valid show ID
- **THEN** the field returns the feed supplied by the iTunes lookup as a regular feed search result

#### Scenario: Pasted website link

- **WHEN** a fetched website advertises a usable RSS alternate link
- **THEN** the field returns the discovered feed as a regular feed search result
