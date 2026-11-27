## ADDED Requirements

### Requirement: Feed directory caching and upstream budget

The server SHALL share in-flight searches by normalized key and cache successful feed directory results for ten minutes in a bounded per-server cache. Text keys SHALL normalize case and whitespace. URL keys SHALL preserve case-sensitive paths and queries.

#### Scenario: Repeated text search

- **WHEN** searches differ only in case or whitespace within ten minutes
- **THEN** later searches receive the cached results without an upstream request

#### Scenario: Concurrent equivalent searches

- **WHEN** a search starts while a search with the same normalized key is in flight
- **THEN** both searches share the upstream work
- **AND** the work continues while at least one caller is waiting for it

#### Scenario: Upstream budget exhausted

- **WHEN** uncached searches would exceed 15 upstream requests in a minute
- **THEN** the server serves any matching cache entry and refuses further required upstream requests immediately with Graph `THROTTLED`
- **AND** optional enrichment may be skipped when the primary results are already available

#### Scenario: Server restarts with an exhausted budget

- **WHEN** the server restarts after persisting its upstream request reservations
- **THEN** unexpired reservations continue to count against the budget until their original deadlines
- **AND** the server sends another upstream request only after durably recording its reservation

#### Scenario: Budget file unreadable

- **WHEN** the server cannot read its persisted upstream request reservations
- **THEN** it refuses the upstream request without sending it
- **AND** a later request retries the read and, once it succeeds, counts the unexpired reservations

### Requirement: Feed directory identity and credentials

The server SHALL send `Beamfall/<version> (+https://beamfall.com/bot)` as the User-Agent on directory requests and feed polls, using the running release version. The optional extended directory SHALL receive requests only when the operator has supplied both its API key and secret.

#### Scenario: Extended directory credentials absent

- **WHEN** the operator has not configured a key
- **THEN** the server uses the primary directory and makes zero requests to the extended directory

### Requirement: Feed discovery from URLs

The existing `searchFeeds` field SHALL accept absolute website URLs. Outbound discovery requests SHALL use the hardened fetcher, bounded response bodies, and the shared upstream budget. Feed URLs from every directory provider and website discovery SHALL pass the public document URL checks before caching or return.

#### Scenario: Malformed website link

- **WHEN** an input starts with an HTTP(S) scheme and fails URL parsing or the public document URL checks
- **THEN** the field returns a localized `INVALID` error without an upstream request

#### Scenario: Pasted Apple Podcasts show link

- **WHEN** a show URL provides a numeric show ID and the iTunes lookup API supplies a usable feed
- **THEN** the plugin resolves it through the server's budgeted fetcher with the iTunes lookup API and the field returns that feed

#### Scenario: Pasted website link

- **WHEN** a fetched website advertises a usable relative RSS alternate link and declares a base URL
- **THEN** the field returns the feed URL resolved against that base, using the final response URL to resolve a relative HTML base

#### Scenario: Unsafe directory hit

- **WHEN** a directory provider returns a feed URL with a disallowed scheme, userinfo, or a credential-like query parameter
- **THEN** the server drops that hit
