# Request management seam for request tools

Ticket: CORE-1698 (`queue:core:main`).

## Why

Request-management tools (the Jellyseerr/Overseerr class) need four things from a media server:
sign users in, ask "is this item in the library?", trigger a scan after a download lands, and
learn when the requested item arrives. Beamfall already has sign-in (the compat
`Users/AuthenticateByName` binding and scoped `/v1` keys), a targeted scan (`POST /v1/scans` with
`libraryID` and `root`) and arrival events (`/v1/events` and the webhook emitter carry
`media.created` and `media.updated`). The stable `/v1` surface cannot answer the availability
question: an external tool knows an item by a provider identifier (a scheme and a value), and `/v1`
exposes no way to find Media by one, nor does a Media read return its identifiers. A late
identifier attach from hydration also emits no event, so a tool watching the stream misses an item
whose identifier arrived after it was created.

## What Changes

- New `GET /v1/media?scheme=&value=` (`read-catalog`) returns the visible Media that carry that
  external identifier, at most 20, each with its identifiers. An absent identifier returns an
  empty list, and hidden Media are omitted exactly as absent ones (no existence oracle).
- `GET /v1/media/{mediaID}` gains an optional `externalIDs` array (at most 32 `{scheme, value}`
  pairs).
- Attaching a new external identifier to existing Media appends `media.updated`, so SSE and
  webhook subscribers see the arrival of an identifier they are waiting for. Re-attaching an
  identifier the Media already has emits nothing.
- Core stays domain-agnostic: schemes are opaque lowercase strings. Mapping a provider's own kinds
  (for example a film versus a series in one identifier space) stays in the domain plugin or the
  tool.
- The client-core-wire contract is published as minor version 2.5.0 (additive). Consumer repins
  are follow-up work.
- `docs/api/request-tools.md` documents the end-to-end request flow on these surfaces.

## Out of scope

- A native request queue inside Beamfall. The seam lets an external tool own requests.
- Scanning a sub-path of a Library root (`POST /v1/scans` accepts only a configured root).
- A Worker-host lookup method for Integration Workers.
