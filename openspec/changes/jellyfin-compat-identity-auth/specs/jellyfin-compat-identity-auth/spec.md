## ADDED Requirements

### Requirement: Compatibility routes report Beamfall server identity

The server SHALL expose GET `System/Info/Public` without credentials and GET `System/Info`
with an integration session granting read access. Both routes SHALL report the product name
`Beamfall`, the running Beamfall build version, the existing stable discovery ID, and the
configured server name when available. Neither response SHALL include library or media data.
Both routes SHALL use the compatibility handler's existing request boundary.

#### Scenario: Public and authenticated identity agree

- **WHEN** a request in the configured trust zone retrieves either route with the appropriate credentials
- **THEN** the server returns the same product name, version, ID and server name on both routes

### Requirement: Compatibility requests authenticate with integration session headers

The server SHALL accept a bearer in `X-Emby-Token` or as a quoted `Token` parameter in a
`MediaBrowser` authorization header in `X-Emby-Authorization` or `Authorization`. It SHALL
return the same 401 status and body for absent, invalid, expired, revoked or insufficiently scoped
credentials. Query parameters SHALL NOT grant access.

#### Scenario: Request presents only a query credential

- **WHEN** a caller sends a valid integration token only in the `api_key` query parameter
- **THEN** the response is identical to the response for a missing credential

### Requirement: Compatibility logout revokes a valid integration session

The server SHALL expose POST `Sessions/Logout` through the compatibility request boundary.
It SHALL accept a valid integration session regardless of its operation scopes, revoke it
durably and return an empty 204 response.

#### Scenario: A refresh-only integration logs out

- **WHEN** a valid integration session granting only refresh access calls logout
- **THEN** logout returns an empty 204 response and later refresh requests using that token return 401

### Requirement: Compatibility library refresh admission is rate limited

The server SHALL expose POST `Library/Refresh` through the existing compatibility request boundary
and require an integration session granting refresh access. The server SHALL admit at most one
refresh request per minute across all callers. The admission deadline SHALL be held in process
memory and reset on restart. Requests within the admission window SHALL return an empty 204
response and enqueue no work.

#### Scenario: Concurrent refresh requests

- **WHEN** several authenticated callers request a refresh within the same minute
- **THEN** only one request is admitted and each caller receives an empty 204 response

### Requirement: Compatibility library refresh uses the durable per-root scan queue

An admitted refresh SHALL use the existing scan queue for every configured root of every eligible
Library and return an empty 204 response. Durable queue deduplication SHALL allow at most one
queued scan per Library and root across admitted requests, including after restart.

#### Scenario: Refresh for multiple Library roots

- **WHEN** a refresh is admitted and no active scan jobs exist for the eligible Library roots
- **THEN** the server queues one scan for each eligible Library root

#### Scenario: Refresh after a restart

- **WHEN** a refresh is admitted after a restart while scans for all eligible Library roots are still queued
- **THEN** the refresh adds no scan jobs and returns an empty 204 response

### Requirement: Compatibility refresh requires operator allowlisting

Eligible Libraries SHALL be visible to the integration session's Profile without Reveal,
unsealed, and on the operator allowlist defined by ADR-0171. Until CORE-2683 supplies the
allowlist, no Library SHALL be eligible.

#### Scenario: No Library is allowlisted

- **WHEN** an authenticated caller requests refresh and there are no allowlisted Libraries
- **THEN** the server returns an empty 204 response and submits no scan job
