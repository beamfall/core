## ADDED Requirements

### Requirement: Feed directory caching and upstream budget

The server SHALL share in-flight searches by normalized key and cache successful feed directory results for ten minutes in a bounded per-server cache. Text keys SHALL normalize case and whitespace. URL keys SHALL preserve case-sensitive paths and queries.

#### Scenario: Repeated text search

- **WHEN** searches differ only in case or whitespace within ten minutes
- **THEN** later searches receive the cached results without an upstream request

#### Scenario: Concurrent equivalent searches

- **WHEN** a search starts while a search with the same normalized key is in flight
- **THEN** both searches share the upstream work

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

The existing `searchFeeds` field SHALL accept absolute website URLs. Website discovery reads RSS alternate links and resolves relative links against the document's effective base URL, using the final response URL to resolve a relative HTML base. Outbound discovery requests SHALL use the hardened fetcher, bounded response bodies, and the shared upstream budget. Feed URLs from every directory provider and website discovery SHALL pass the public document URL checks before caching or return.

#### Scenario: Malformed website link

- **WHEN** an input starts with an HTTP(S) scheme but does not parse as an absolute URL with a host
- **THEN** the field returns a localized `INVALID` error without an upstream request

#### Scenario: Pasted website link

- **WHEN** a fetched website advertises a usable relative RSS alternate link and declares a base URL
- **THEN** the field returns the feed URL resolved against that base

#### Scenario: Unsafe directory hit

- **WHEN** a directory provider returns a feed URL with a disallowed scheme, userinfo, or a credential-like query parameter
- **THEN** the server drops that hit
