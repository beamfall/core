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

### Requirement: Compatibility library refresh is bounded

The server SHALL expose POST `Library/Refresh` through the existing compatibility request boundary
and require an integration session granting refresh access. The server SHALL admit at most one
refresh submission per minute, using the existing scan queue. Authenticated requests SHALL
return 204 whether admitted or rate limited.

#### Scenario: Concurrent refresh requests

- **WHEN** several authenticated callers request a refresh within the same minute
- **THEN** at most one request submits scan work and each receives an empty 204 response
