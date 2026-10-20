# Beamfall Developer API — reference

Version 1.1.0 · OpenAPI 3.1.0 · generated from `docs/api/openapi.json` by `script/gen-api-docs.sh` — do not edit by hand.

The Beamfall `/v1/` stable developer contract (ADR-0040). Every endpoint under `/v1/` is part of the stability promise: no breaking changes within this major version; a breaking change requires a new prefix (`/v2/`) with a minimum two-release deprecation window. Endpoints are authenticated with a scoped developer API key (`Authorization: Bearer bfk_...`) carrying a subset of the closed API-scope vocabulary; each operation declares the single scope it requires. All catalog and session reads are Reveal-filtered and Profile-bound to the key's binding (ADR-0004): a Profile-bound key never sees content or sessions outside its Profile, and restricted content it cannot Reveal never appears. The wire entity is `Media`; this surface is domain-agnostic (no plugin/domain vocabulary).

Server: `/` — The Beamfall home server origin.

## Authentication

**scopedKey** (http, bearer, format `bfk_<id>_<secret>`): A scoped developer API key (ADR-0040 §2.2), minted by a server operator from the Console with a chosen scope subset and an optional Profile binding. Presented as `Authorization: Bearer bfk_...`. A session credential or an admin API token carries no API scopes and is rejected by every `/v1/` endpoint.

## Endpoints

### GET `/v1/libraries`

List the Libraries visible to the key.

Required scope: `read-catalog`

Requires the `read-catalog` scope. Reveal-filtered and Profile-bound: a Profile-bound key sees only its Profile's granted Libraries, and a restricted Library it has not Revealed never appears.

**Parameters**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `cursor` | query | string | no | Opaque cursor returned as nextCursor by the previous page. |
| `limit` | query | integer | no | Maximum number of Libraries to return. |

**Responses**

| Status | Description | Body |
| --- | --- | --- |
| 200 | The visible Libraries. | object {`libraries`: array of [Library](#library), `nextCursor`: string} |
| 401 | No valid credential was presented. | [Error](#error) |
| 403 | The scoped key does not carry the scope this operation requires. | [Error](#error) |

### GET `/v1/libraries/{libraryID}/media`

List the Media in one Library.

Required scope: `read-catalog`

Requires the `read-catalog` scope. Reveal-filtered: a Library outside the key's grants (or restricted and unrevealed) yields an empty list, never an error.

**Parameters**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `libraryID` | path | string | yes |  |
| `cursor` | query | string | no | Opaque cursor returned as nextCursor by the previous page. |
| `limit` | query | integer | no | Maximum number of Media items to return. |

**Responses**

| Status | Description | Body |
| --- | --- | --- |
| 200 | The Media in the Library, Reveal-filtered. | object {`media`: array of [MediaSummary](#mediasummary), `nextCursor`: string} |
| 400 | libraryID missing. | [Error](#error) |
| 401 | No valid credential was presented. | [Error](#error) |
| 403 | The scoped key does not carry the scope this operation requires. | [Error](#error) |

### GET `/v1/media/{mediaID}`

Read one Media's detail.

Required scope: `read-catalog`

Requires the `read-catalog` scope. Reveal-filtered: a hidden or unknown id both resolve to 404, so the read is never an existence oracle for Media the key cannot see.

**Parameters**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `mediaID` | path | string | yes |  |

**Responses**

| Status | Description | Body |
| --- | --- | --- |
| 200 | The Media detail. | [MediaDetail](#mediadetail) |
| 401 | No valid credential was presented. | [Error](#error) |
| 403 | The scoped key does not carry the scope this operation requires. | [Error](#error) |
| 404 | Media not found or not visible. | [Error](#error) |

### GET `/v1/sessions`

List active playback sessions.

Required scope: `read-sessions`

Requires the `read-sessions` scope. A Profile-bound key sees only its own Profile's sessions; an admin-scoped key (no Profile binding) sees all. Media-identifying fields are omitted for sessions playing Media the key cannot currently Reveal.

**Parameters**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `cursor` | query | string | no | Opaque cursor returned as nextCursor by the previous page. |
| `limit` | query | integer | no | Maximum number of active sessions to return. |

**Responses**

| Status | Description | Body |
| --- | --- | --- |
| 200 | The active sessions. | object {`sessions`: array of [Session](#session), `nextCursor`: string} |
| 401 | No valid credential was presented. | [Error](#error) |
| 403 | The scoped key does not carry the scope this operation requires. | [Error](#error) |

### POST `/v1/playback-control`

Issue a transport command to an active playback session.

Required scope: `playback-control`

Requires the `playback-control` scope. Publishes one closed-vocabulary transport command (`pause`, `resume`, `skip-media`, `skip-track`) to the target session's event channel; success means the command was authorized and published, not that the target device has applied it. A Profile-bound key can control only its own Profile's reveal-visible active sessions. An admin-scoped key (no Profile binding) can control reveal-visible active sessions across Profiles, matching ADR-0040's operator-created all-session automation key model. Missing, cross-Profile, inactive, and reveal-hidden targets all resolve to 404.

**Request body** (`application/json`)

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `sessionID` | string | yes |  |
| `command` | string (`pause`, `resume`, `skip-media`, `skip-track`) | yes |  |

**Responses**

| Status | Description | Body |
| --- | --- | --- |
| 202 | The transport command was accepted for delivery. | [PlaybackControlResponse](#playbackcontrolresponse) |
| 400 | sessionID missing, command missing or outside the closed transport vocabulary, or invalid JSON. | [Error](#error) |
| 401 | No valid credential was presented. | [Error](#error) |
| 403 | The scoped key does not carry the scope this operation requires. | [Error](#error) |
| 404 | Session not found, not active, outside the key's Profile binding, or not reveal-visible. | [Error](#error) |

### POST `/v1/scans`

Trigger a library scan/import.

Required scope: `ingest`

Requires the `ingest` scope. Enqueues a durable scan job for a Library the key can see; an empty root scans the Library's persisted roots. A Library outside the key's grants is reported as 404 (never an existence oracle).

**Request body** (`application/json`)

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `libraryID` | string | yes | The Library to scan. Must be visible to the key. |
| `root` | string | no | Optional root path. Empty scans the Library's persisted roots. |

**Responses**

| Status | Description | Body |
| --- | --- | --- |
| 202 | The scan job(s) were enqueued. | object {`jobIDs`: array of string} |
| 400 | libraryID missing, the Library has no roots, or more than 200 roots require selecting one root. | [Error](#error) |
| 401 | No valid credential was presented. | [Error](#error) |
| 403 | The scoped key does not carry the scope this operation requires. | [Error](#error) |
| 404 | Unknown or invisible Library. | [Error](#error) |

### GET `/v1/backups/{libraryID}/status`

Read backup ingest status for one Library.

Required scope: `backup`

Requires the `backup` scope and a Profile-bound key. Returns the caller Profile's pause/quota state for the target Library; paused state is explicit so a client can surface that backup is stopped instead of silently dropping uploads.

**Parameters**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `libraryID` | path | string | yes |  |

**Responses**

| Status | Description | Body |
| --- | --- | --- |
| 200 | Backup ingest status. | [BackupStatus](#backupstatus) |
| 400 | libraryID missing. | [Error](#error) |
| 401 | No valid credential was presented. | [Error](#error) |
| 403 | The scoped key does not carry the scope this operation requires. | [Error](#error) |
| 500 | Backup status could not be read due to an internal failure. | [Error](#error) |

### POST `/v1/backups/media`

Upload one profile-owned backup Media blob.

Required scope: `backup`

Requires the `backup` scope and a Profile-bound key. The request is multipart/form-data with `libraryID`, optional `title`, `class`, `container`, `codec`, and a `file` part. The server applies quota and disk-headroom checks, rejects hostile image headers for image uploads, encrypts the uploaded bytes before durable storage, and registers Media through the target Library catalog plane.

**Responses**

| Status | Description | Body |
| --- | --- | --- |
| 201 | The upload was encrypted, deduplicated by content fingerprint, and registered as Media. | [BackupMediaResponse](#backupmediaresponse) |
| 400 | Invalid multipart request, missing fields, hostile image bytes, or rejected backup policy. | [Error](#error) |
| 401 | No valid credential was presented. | [Error](#error) |
| 403 | The scoped key does not carry the scope this operation requires. | [Error](#error) |
| 423 | Backup ingest is paused and must be surfaced to the user. | [BackupMediaResponse](#backupmediaresponse) |
| 500 | Backup ingest failed due to an internal storage failure. | [Error](#error) |
| 507 | Quota or disk-headroom guard refused the upload. | [Error](#error) |

### GET `/v1/events`

Subscribe to the server-sent event stream.

Required scope: `events-subscribe`

Requires the `events-subscribe` scope. A long-lived `text/event-stream` of Reveal-filtered server events (playback, library, and — for admin-scoped keys — Profile lifecycle). Event payloads carry only identifiers and state; a restricted-Media event never reaches a key that cannot Reveal it. This is an alias of the pre-existing `/api/events` route on the stable prefix; both resolve through the same `events-subscribe` gate. Reconnect with the standard `Last-Event-ID` header or the `?after=` query cursor.

**Parameters**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `after` | query | integer | no | Resume cursor: deliver only events after this sequence number. |

**Responses**

| Status | Description | Body |
| --- | --- | --- |
| 200 | The SSE stream. | — |
| 401 | No valid credential was presented. | [Error](#error) |
| 403 | The scoped key does not carry the scope this operation requires. | [Error](#error) |

## Schemas

### Library

A Reveal-filtered Library visible to the key's Profile binding.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `id` | string | yes |  |
| `title` | string | yes |  |
| `restricted` | boolean | yes | True for a restricted Library (only present when the key has Revealed it). |

### MediaSummary

A Media item in a library feed. Generic, plugin-agnostic fields only.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `id` | string | yes |  |
| `title` | string | yes |  |
| `summary` | string | no |  |

### MediaDetail

Full detail for one Media row, Reveal-filtered. A hidden or unknown id resolves to 404 (never an existence oracle).

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `id` | string | yes |  |
| `libraryID` | string | yes |  |
| `title` | string | yes |  |
| `summary` | string | no |  |
| `year` | integer | no |  |
| `durationSeconds` | integer | no |  |

### Session

One active playback session. Media-identifying fields (mediaID/title/libraryID) are omitted when the key cannot currently see that Media under its Reveal filter.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `sessionID` | string | yes |  |
| `profileID` | string | yes |  |
| `deviceName` | string | no |  |
| `mediaID` | string | no |  |
| `title` | string | no |  |
| `libraryID` | string | no |  |
| `transcoding` | boolean | yes |  |
| `positionSeconds` | integer | no |  |
| `durationSeconds` | integer | no |  |

### PlaybackControlRequest

A closed-vocabulary transport command for one active playback session. Target and Media visibility failures all resolve to 404 to avoid session/Profile/restricted-Media oracles.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `sessionID` | string | yes |  |
| `command` | string (`pause`, `resume`, `skip-media`, `skip-track`) | yes |  |

### PlaybackControlResponse

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `sessionID` | string | yes |  |
| `command` | string (`pause`, `resume`, `skip-media`, `skip-track`) | yes |  |

### ScanRequest

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `libraryID` | string | yes | The Library to scan. Must be visible to the key. |
| `root` | string | no | Optional root path. Empty scans the Library's persisted roots. |

### BackupStatus

Profile-bound backup ingest status for one Library. Paused is explicit so clients never silently drop uploads.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `libraryID` | string | yes |  |
| `paused` | boolean | yes |  |
| `quotaBytes` | integer | yes |  |

### BackupMediaResponse

Result of a profile-owned backup upload registered as Media.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `mediaID` | string | no |  |
| `libraryID` | string | yes |  |
| `fingerprint` | string | no |  |
| `paused` | boolean | no |  |

### Error

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `error` | string | yes |  |
