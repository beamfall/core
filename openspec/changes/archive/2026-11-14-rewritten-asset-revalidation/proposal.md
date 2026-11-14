# Base-path-rewritten web assets revalidate instead of reloading

Ticket: CORE-1161 (`queue:core:main`).

## Why

Under a configured base path (`BEAMFALL_BASE_URL` with a path) the server rewrites the absolute
paths inside the web bundle's text files. Those bytes depend on runtime configuration, so the
files cannot be served as immutable (`web-asset-delivery`). The quality sweep (CORE-1171 and
siblings) made the server rewrite each file once at startup and give every compressible file a
strong `ETag`, but nothing states that a rewritten file must carry one, no test covers a rewritten
file, and the response says nothing about how to cache it, so each browser decides for itself
whether to keep the file at all.

## What Changes

- A rewritten file is served with `Cache-Control: no-cache` and a strong `ETag` per representation:
  the browser keeps it and asks once per load whether it changed.
- A matching `If-None-Match` is answered `304` with no body.
- The rewrite happens once per process, not per request.

## Wire compatibility

Additive response headers on `/web/` assets under a base path. No API change.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `web-asset-delivery`: adds the revalidation requirement for rewritten files.
