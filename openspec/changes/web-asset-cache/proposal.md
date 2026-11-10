# Cache and compress the embedded web UI assets

Ticket: CORE-0963 (`queue:core:main`).

## Why

The server sends the embedded web UI's content-hashed bundle files with no `Cache-Control` header
and no compression, and the service worker keeps only the entry chunks. Every visit therefore
revalidates or refetches the bundle, and every lazy route chunk is fetched again at full size.

## What Changes

- Content-hashed files under `/web/assets/` are served with
  `Cache-Control: public, max-age=31536000, immutable`.
- Text assets (JS, CSS, HTML, JSON, web manifest, SVG, source maps) are served gzip-encoded when
  the request accepts gzip. Each file is compressed once per process.
- The service worker stores each successful same-origin `/web/assets/` response it fetches, so a
  lazy chunk is served from its cache afterwards.

## Non-goals

- Brotli. It needs a new module dependency; gzip uses the standard library.
- Long-lived caching of files the server rewrites for a configured base path. Their bytes depend on
  runtime configuration, so they stay revalidated.
- Changing the shell, `sw.js` or manifest caching rules.

## Failure modes

- A missing asset must not be cached: unknown `/web/assets/` paths stay `404` with no cache header.
- A `Range` request is served unencoded so byte ranges keep their meaning.
- A client that refuses gzip (`q=0` or no `Accept-Encoding`) receives the identity body.

## Acceptance evidence

- `internal/server/web_cache_test.go` covers the cache header, the gzip and identity bodies, the
  `Range` fallback and the missing-asset case.
- `internal/web/app/sw-plugin.test.ts` covers storing a `200` and skipping `404`, opaque and
  non-asset responses.

## Rollback

Revert the change. No stored state or wire contract depends on it; clients fall back to
revalidating as before, and the versioned service-worker cache is dropped on the next activation.

## Capabilities

### New Capabilities

- `web-asset-delivery`: how the server and service worker cache and encode the embedded web UI.

### Modified Capabilities

None.
