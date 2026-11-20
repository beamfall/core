## MODIFIED Requirements

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

## ADDED Requirements

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
access. It SHALL decode a bounded JSON body listing updated paths, reject a malformed or oversized
body with 400, and share the refresh admission limit with `Library/Refresh`.

#### Scenario: Oversized update body

- **WHEN** an authenticated caller posts a body larger than the decode bound
- **THEN** the server returns 400 and queues no scan

### Requirement: Compatibility media updates refresh only the containing roots

An admitted media update SHALL queue one scan per eligible Library root that equals or contains an
updated absolute path, through the existing per-root scan queue, and SHALL queue nothing for paths
outside every eligible root. A well-formed request SHALL return an empty 204 response whether or
not any root matched.

#### Scenario: Two updates under one root

- **WHEN** an admitted request lists two paths under the same eligible Library root
- **THEN** the server queues one scan for that root and none for any other root

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
