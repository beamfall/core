# Public client API: /v1 serves enrolled client devices

Ticket: CORE-1694 (`queue:core:main`).
Decision: ADR-0234 (proposed), which extends ADR-0040.

## Why

Nobody outside the project can build a Beamfall client. The first-party clients use the private
GraphQL surface, which changes with them and carries no stability promise. The stable `/v1` surface
accepts only operator-minted scoped developer keys and has no playback or progress operations, so a
client app on many devices cannot use it.

## What Changes

- Every `/v1` route contract declares the credentials it accepts: `scopedKey` (a developer key
  holding the route's scope, unchanged) and `deviceSession` (an enrolled device Session acting as its
  Profile). The scope vocabulary stays at five scopes.
- The three catalog reads (`listLibraries`, `listLibraryMedia`, `getMedia`) accept both credentials.
- New `POST /v1/media/{mediaID}/playback-info` returns the playback decision for a declared
  capability profile: stream URLs, tokens and expiries, renditions, caption tracks and the resume
  position. It accepts `deviceSession` only.
- New `PUT /v1/media/{mediaID}/progress` records a playback position with the same resume policy
  and watched transition as the first-party clients. It accepts `deviceSession` only.
- Automation routes (sessions list, playback control, scans, backups, events) stay `scopedKey` only.
- `docs/api/openapi.json` gains a `deviceSession` security scheme, the two operations and an
  `x-credentials` list on every operation; `info.version` goes to 1.2.0. The wire-contract golden
  records the new routes and credentials.
- Responses are dedicated projections, not internal structs, with explicit list bounds.

## Non-goals

- Enrollment over `/v1`, the device-authorization flow, favourites, session control and server
  discovery (later slices of ADR-0234).
- Generating the SDKs and the generation gate check (ADR-0234 D5, a follow-up ticket).
- Opening any automation route to device Sessions.
- Moving the first-party clients to `/v1`.

## Failure modes

- An admin API token, an integration Session or a scoped key on a client-only route answers 403.
- A device Session on an automation route answers 403.
- Unknown or hidden Media answers 404 on both new routes, so a client cannot probe hidden items.
- Negative positions, durations or event times answer 400.
- A server without the playback service or the progress recorder answers 503.

## Acceptance evidence

- `internal/server` tests cover each credential on each route class, the playback-info projection,
  the progress write with its watched transition, and hidden-Media 404s.
- `TestOpenAPISpecMatchesRoutes` checks that every route and its credentials match the OpenAPI
  document.
- The wire-contract golden is regenerated with `make regen-wire-goldens` and published.
- `openspec validate --all --strict` passes.
