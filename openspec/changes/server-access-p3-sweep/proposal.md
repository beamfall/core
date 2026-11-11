# Tighten byte-route CORS, offline-pack content opens and partial scan responses

Ticket: CORE-0735 (`queue:core:main`).

## Why

Three small server findings from the 2026-08-22 core API audit still hold on main:

- `/api/media/artwork/` and `/api/entity/artwork/` are in the token-byte cross-origin list
  (`tokenByteGETPath`), so a foreign origin receives `Access-Control-Allow-Origin: *`, but both
  routes authenticate by session credential, not by a URL token. The list's invariant (every entry
  is authorized by a signed token in the path) no longer holds.
- `/api/pack/content/` opens the stored ciphertext path by name. Every other byte route binds the
  served file to a root and refuses a symlinked or swapped final component.
- `POST /v1/scans` enqueues one job per root. When an enqueue fails part-way, the reply is a bare
  `500` error, and the caller cannot learn which jobs were already queued.

## What Changes

- Artwork and entity-artwork GETs leave the token-byte cross-origin list. A foreign-origin request
  to them gets the private-UI same-origin policy (`403`, no allow-origin header). Same-origin and
  originless (native client, `<img>`) requests are unchanged.
- Offline-pack content is opened beneath the server-owned pack root. The stored path must name a
  file directly inside that pack's directory; the final component must be a regular file (a symlink
  is refused), and the opened descriptor must be the same file that was checked. Failures answer
  `404`, like a missing member.
- A `/v1/scans` enqueue failure still answers `500`, and the error body now also carries `jobIDs`:
  the jobs queued before the failure (possibly empty). `docs/api/openapi.json` documents it.

## Non-goals

- Artwork `Cache-Control` and the duplicate HLS-segment store reads: they live in
  `internal/mediahttp`, which this change does not touch.
- Trimming the unauthenticated `/health` payload or restricting `internal-only` routes.
- Keyset pagination for `/v1` list routes.

## Failure modes

- A web client served from another origin that reads artwork bytes cross-origin loses access. No
  such client exists: a credentialed cross-origin read was already blocked by `*`, and header
  credentials need a preflight the private-UI policy refuses.
- A pack whose directory or member was replaced by a symlink stops serving (`404`) instead of
  serving bytes from outside the pack root.

## Acceptance evidence

- `internal/server/route_class_test.go`: foreign-origin artwork GETs are refused without an
  allow-origin header; the token-byte list test excludes the artwork routes.
- `internal/offlinepack/http_test.go`: symlinked member and symlinked pack directory are refused.
- `internal/server/v1_test.go`: a mid-loop enqueue failure answers `500` with the queued job IDs.
- `go test -race ./internal/server/ ./internal/offlinepack/`.

## Rollback

Revert the change. No stored state depends on it; the `/v1/scans` error body gains a field only.

## Capabilities

### New Capabilities

- `server-byte-route-access`: cross-origin policy and file binding for byte routes, and partial
  failure reporting for `/v1/scans`.

### Modified Capabilities

None.
