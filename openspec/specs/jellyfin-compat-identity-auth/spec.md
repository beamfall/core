# jellyfin-compat-identity-auth Specification

## Purpose
Let automation tools that speak the Jellyfin API (Seerr, Radarr, Sonarr, Lidarr, dashboards) identify a Beamfall server, authenticate with an integration session token and ask for a library refresh, without exposing any streaming or playback route, Reveal-restricted data, or a mutation beyond queueing a scan (ADR-0171).

## Requirements

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

The server SHALL accept a bearer in `X-Emby-Token`, in `X-MediaBrowser-Token`, or as a quoted
`Token` parameter in a `MediaBrowser` authorization header in `X-Emby-Authorization` or
`Authorization`. It SHALL return the same 401 status and body for absent, invalid, expired, revoked
or insufficiently scoped credentials. Query parameters SHALL NOT grant access.

#### Scenario: Request presents only a query credential

- **WHEN** a caller sends a valid integration token only in the `api_key` query parameter
- **THEN** the response is identical to the response for a missing credential

#### Scenario: Request presents the MediaBrowser token header

- **WHEN** a caller sends a valid integration token granting read access in `X-MediaBrowser-Token`
- **THEN** read-scoped routes accept the request

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

### Requirement: Compatibility notifier probes disclose no server paths

The server SHALL expose GET `System/Configuration`, GET `Library/MediaFolders` and GET `Items`
with an integration session granting read access. `System/Configuration` SHALL return an empty
JSON object. `Library/MediaFolders` and `Items` SHALL return an empty item list with a total count
of zero, so no server filesystem path or Library data is disclosed.

#### Scenario: A tool looks up the server path of a changed item

- **WHEN** an authenticated caller requests `Items` with a path field
- **THEN** the server returns an empty item list

### Requirement: Compatibility admin notifications are unsupported

The server SHALL authenticate POST `Notifications/Admin` with read access and then return 404,
without recording or delivering any notification.

#### Scenario: A tool sends an admin notification

- **WHEN** an authenticated caller posts an admin notification
- **THEN** the server returns 404 and stores nothing

#### Scenario: A tool sends an admin notification with a bad token

- **WHEN** a caller posts an admin notification with an invalid token
- **THEN** the server returns the uniform 401 response

### Requirement: Compatibility media update requests are bounded

The server SHALL expose POST `Library/Media/Updated` with an integration session granting refresh
access. It SHALL decode a bounded JSON body listing updated paths, reject a malformed, oversized or
trailing-data body with 400, and share the refresh admission limit with `Library/Refresh`.

#### Scenario: Oversized update body

- **WHEN** an authenticated caller posts a body larger than the decode bound
- **THEN** the server returns 400 and queues no scan

### Requirement: Compatibility media updates refresh only the containing roots

An admitted media update SHALL queue one scan per eligible Library root that equals or contains an
updated absolute path, through the existing per-root scan queue, and SHALL queue nothing for paths
outside every eligible root. A well-formed request SHALL return an empty 204 response whether or
not any root matched. Matching SHALL use the session scope read after the body arrives and SHALL run
after the response is written.

#### Scenario: Two updates under one root

- **WHEN** an admitted request lists two paths under the same eligible Library root
- **THEN** the server queues one scan for that root and none for any other root

#### Scenario: Session revoked during the upload

- **WHEN** the integration session is revoked while its media update body is arriving
- **THEN** the server returns the uniform 401 and queues no scan

#### Scenario: Update outside every root

- **WHEN** an admitted request lists only a path outside every eligible Library root
- **THEN** the server queues no scan and returns an empty 204 response

### Requirement: Compatibility notifier routes accept the MediaBrowser alias

The server SHALL serve `Notifications/Admin`, `Library/MediaFolders`, `Library/Media/Updated` and
`Library/Refresh` also under the `mediabrowser/` path segment of the compatibility prefix, with the
same authentication and behavior. No streaming or playback route SHALL be added under either path.

#### Scenario: Lidarr 3.x posts a media update

- **WHEN** an authenticated caller posts to `mediabrowser/Library/Media/Updated`
- **THEN** the server handles it exactly as `Library/Media/Updated`

### Requirement: Compatibility sign-in describes the signed-in user

A successful `Users/AuthenticateByName` response SHALL carry the integration token, its expiry, the
server ID, and a `User` object with a user ID, a display name, the server ID, the configured server
name and `Policy.IsAdministrator`. For a Profile sign-in the user ID and name SHALL be the Profile's.
`IsAdministrator` SHALL be true only for the admin setup session. The response SHALL NOT include any
other Profile, Library or media data.

#### Scenario: Admin setup sign-in

- **WHEN** a caller signs in with the admin name and the admin password
- **THEN** the response reports `IsAdministrator` true, the server ID shown by `System/Info/Public`,
  and the setup account ID, not the owner Profile's ID

#### Scenario: Opted-in Profile sign-in

- **WHEN** a caller signs in with an opted-in adult Profile and its entry PIN
- **THEN** the response reports `IsAdministrator` false and the Profile's own ID and display name

### Requirement: The admin setup sign-in reports a setup account

The `User` object of the admin setup sign-in SHALL describe a setup account, not the owner Profile:
the name `admin` and a user ID derived from the server ID that is stable across sign-ins and differs
from every Profile ID.

#### Scenario: The owner signs in with a PIN after setup

- **WHEN** the owner Profile signs in with its entry PIN after an admin setup sign-in
- **THEN** the two responses report different user IDs, and a second admin sign-in reports the first one's ID

### Requirement: Only the admin setup session carries the setup scope

The server SHALL grant the `compat.setup` integration scope only to a session created by the admin
setup sign-in, and SHALL refuse a sign-in whose caller requests it. The store SHALL refuse to create
a session with that scope unless it belongs to the owner Profile, is created under the admin
sign-in's throttle reservation, and expires no later than 15 minutes after that reservation. A
setup session SHALL end when ownership moves.

#### Scenario: A Profile session asks for the setup scope

- **WHEN** an integration session is created for an opted-in Profile with the setup scope requested
- **THEN** creation fails and no session is stored

#### Scenario: The setup window closes during sign-in

- **WHEN** 15 minutes pass between the admin sign-in's reservation and creating its session
- **THEN** the sign-in fails and no session is stored

#### Scenario: Ownership moves during setup

- **WHEN** ownership moves to another adult Profile while the former owner's setup session is live
- **THEN** that setup session no longer authenticates, and the former owner's other integration
  sessions are unaffected

### Requirement: Compatibility API key routes never mint a credential

The server SHALL expose POST `Auth/Keys` and GET `Auth/Keys` only to an integration session granting
setup access, and SHALL return the uniform 401 to every other credential. POST SHALL return 204 and
create nothing. GET SHALL list exactly one key: the caller's own token, named after the `Client`
parameter of the caller's MediaBrowser authorization header. The listed token SHALL stop
authenticating when the setup session expires.

#### Scenario: Seerr asks for an API key during setup

- **WHEN** a setup session posts `Auth/Keys?App=Seerr` and then lists `Auth/Keys` with client `Seerr`
- **THEN** the list holds one key named `Seerr` whose token is the setup session's own token

#### Scenario: A Profile session lists API keys

- **WHEN** an opted-in Profile's integration session lists `Auth/Keys`
- **THEN** the server returns the uniform 401

#### Scenario: The listed key after the setup window

- **WHEN** the setup session's 15 minute lifetime has passed
- **THEN** the token listed by `Auth/Keys` gets the uniform 401 on every compatibility route
