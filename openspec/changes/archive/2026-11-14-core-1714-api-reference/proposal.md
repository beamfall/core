# Serve the developer API reference

Ticket: CORE-1714 (`queue:core:main`).

## Why

The `/v1` developer contract lives in `docs/api/openapi.json`, but a running server did not publish
it. Integrators had to find the repository to read the API their own server offers.

## What Changes

- `GET /api/docs/openapi.json` serves the OpenAPI document. Its bytes are `docs/api/openapi.json`,
  embedded at build time, so the served document is the one the contract checks validate.
- `GET /api/docs` serves a browsable HTML reference rendered from that document: no script, a
  restrictive Content-Security-Policy.
- Both routes are `internal-only` (no credential, no library content), outside `/v1/`.

## Non-goals

- No change to the `/v1` contract itself, and no promotion of these routes into `/v1/`.
