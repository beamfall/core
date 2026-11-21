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
JSON object. `Items` with neither a `ParentId` nor an `ids` parameter SHALL return an empty item
list with a total count of zero. No compatibility response SHALL include a server filesystem path.

#### Scenario: A tool looks up the server path of a changed item

- **WHEN** an authenticated caller requests `Items` with a path field and no parent or ids
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

### Requirement: Relative-path media updates refresh every eligible root

When every path in an admitted media update is relative, the server SHALL queue a scan of every
eligible Library root instead of matching paths against roots.

#### Scenario: Lidarr posts relative paths

- **WHEN** an admitted request lists only paths relative to the caller's own root folder
- **THEN** the server queues a scan for every eligible Library root

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

### Requirement: Compatibility user enumeration lists opted-in Profiles

With an integration session granting read access, the server SHALL serve GET `Users` as the list
of enabled Profiles opted in to compatibility sign-in, each with only its ID and display name.
GET `Users/{id}` SHALL describe one such Profile with an empty `Configuration.GroupedFolders`.
GET `Users/{id}/Images/Primary` SHALL return that Profile's stored picture, or 404 when it has
none. Any other user ID SHALL get 404.

#### Scenario: Seerr imports users

- **WHEN** an authenticated caller lists `Users`
- **THEN** each opted-in enabled Profile appears once and no other Profile appears

#### Scenario: A Profile without a picture

- **WHEN** a caller requests the primary image of an opted-in Profile with no stored picture
- **THEN** the server returns 404

### Requirement: Compatibility media folders list eligible Libraries

GET `Library/MediaFolders` SHALL list each eligible Library the session's Profile can see without
Reveal as a collection folder with its ID, name and the collection type that the Library plugin's
interop declaration maps the Library's scope to. A Library whose scope has no mapping, an
encrypted Library and a Library off the operator allowlist SHALL NOT be listed. No folder SHALL
carry a path.

#### Scenario: One allowlisted and one restricted Library

- **WHEN** the allowlist holds a visible Library and a Reveal-restricted Library
- **THEN** only the visible Library is listed and the total count is one

### Requirement: Compatibility item types come from plugin interop declarations

The server SHALL derive each item's type from the interop declaration of the Library's plugin: a
Media item's type from its declared media field value, and a container's type from its declared
kind. Core SHALL compare declared tokens for equality and SHALL NOT name a domain type. An item
with no declared mapping SHALL NOT appear in any compatibility response.

#### Scenario: A plugin declares no interop

- **WHEN** an eligible Library's plugin declares no interop mapping
- **THEN** its Library and items do not appear in any compatibility response

### Requirement: Compatibility items are filtered before counting and paging

`Items?ParentId=<library>` SHALL return the top-level items of one eligible Library: the topmost
mapped container of each visible Media item, or the Media item itself when it has none, filtered by
`IncludeItemTypes`. Reveal, certification and the allowlist SHALL apply before the total count and
paging. Items SHALL be ordered by title and then ID, `Limit` SHALL be capped at 1000, and an
unbounded request SHALL return at most 50,000 items while reporting the true total.

#### Scenario: Paging is stable

- **WHEN** a caller pages a Library with `StartIndex` and `Limit` and the Library does not change
- **THEN** the pages are disjoint, cover every item once and report the same total

#### Scenario: A certification-restricted item

- **WHEN** a Library holds an item above the session Profile's certification ceiling
- **THEN** that item is absent from every page and from the total count

### Requirement: Compatibility item lookups use one uniform miss

`Items?ids=`, `Items/Latest?ParentId=`, `Shows/{id}/Seasons` and `Shows/{id}/Episodes` SHALL return
only items visible to the session's Profile in eligible Libraries. `Items/Latest` SHALL return the
newest visible Media items, at most 100. Seasons and episodes SHALL be ordered by their declared
ordinal. A parent or container ID that is unknown, not visible or outside every eligible Library
SHALL get the same 404, and an unmatched ID in `ids` SHALL be omitted.

#### Scenario: A Reveal-restricted item by ID

- **WHEN** a caller requests `Items?ids=` with the ID of an item in a Reveal-restricted Library
- **THEN** the response holds no items and a total count of zero

#### Scenario: An unknown show

- **WHEN** a caller requests the seasons of an ID that names no visible container
- **THEN** the server returns 404 with the same body as for any other miss

### Requirement: Compatibility items carry provider IDs and video size only

Each item SHALL carry a `ProviderIds` object, possibly empty, holding the stored external IDs and
the ID from the scraper that identified the item when that scraper's lookup is a by-ID lookup.
A Media item SHALL carry one media source with one video stream holding only its stored width and
height. No item SHALL carry a path, file name, stream URL, artwork or watch state.

#### Scenario: An item identified by a by-ID scraper

- **WHEN** an item was identified by a scraper whose lookup is the provider's by-ID lookup
- **THEN** its `ProviderIds` holds that provider's ID
