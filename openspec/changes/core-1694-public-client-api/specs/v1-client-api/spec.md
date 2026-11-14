## ADDED Requirements

### Requirement: Route credentials

Every `/v1` route contract SHALL declare the credentials it accepts from the closed set
`scopedKey` and `deviceSession`, and the server SHALL reject any other credential on that route with
403.

#### Scenario: Device Session reads the catalog

- **WHEN** an enrolled device Session calls `GET /v1/libraries`
- **THEN** the server answers 200 with the Libraries that Session's Profile can see

#### Scenario: Device Session calls an automation route

- **WHEN** an enrolled device Session calls `POST /v1/scans`
- **THEN** the server answers 403

#### Scenario: Scoped key calls a client route

- **WHEN** a scoped developer key calls `POST /v1/media/{mediaID}/playback-info`
- **THEN** the server answers 403

#### Scenario: Responses are not stored by caches

- **WHEN** any `/v1` route answers
- **THEN** the response carries `Cache-Control: no-store`

#### Scenario: Browser client preflight

- **WHEN** a browser sends a CORS preflight for a `/v1` route
- **THEN** the server allows `PUT` and the `X-Beamfall-Session`, `X-Beamfall-Device` and
  `X-Beamfall-Device-Secret` headers

#### Scenario: Integration Session or admin token

- **WHEN** an integration Session or an admin API token calls a route that accepts `deviceSession`
- **THEN** the server does not treat it as a device Session and answers 401 or 403

### Requirement: Playback info

`POST /v1/media/{mediaID}/playback-info` SHALL return the playback decision for the capability
profile in the request, as the device Session's Profile, with stream URLs, stream tokens and
expiries, renditions, caption tracks, the duration and the resume position.

#### Scenario: Visible Media

- **WHEN** a device Session requests playback info for Media its Profile can see
- **THEN** the server answers 200 with the decision and at least one stream

#### Scenario: Hidden or unknown Media

- **WHEN** a device Session requests playback info for Media its Profile cannot see
- **THEN** the server answers 404

### Requirement: Progress reporting

`PUT /v1/media/{mediaID}/progress` SHALL record the reported position for the device Session's
Profile with the resume policy and watched transition the first-party clients use, and SHALL return
the stored position and watched state.

#### Scenario: Finished playback

- **WHEN** a device Session reports `finished: true` for visible Media
- **THEN** the server answers 200 with `watched: true` and the watched transition fires once

#### Scenario: Invalid report

- **WHEN** a report omits `positionSeconds`, sends it as null, or carries a negative position,
  duration or event time
- **THEN** the server answers 400 and writes nothing

#### Scenario: Hidden Media

- **WHEN** a device Session reports progress for Media its Profile cannot see
- **THEN** the server answers 404 and writes nothing

### Requirement: Documented contract

`docs/api/openapi.json` SHALL document every mounted `/v1` route with its accepted credentials, and
a scope for every route that accepts `scopedKey`.

#### Scenario: Route without documentation

- **WHEN** a `/v1` route is mounted without a matching OpenAPI operation, or with different
  credentials
- **THEN** `TestOpenAPISpecMatchesRoutes` fails
