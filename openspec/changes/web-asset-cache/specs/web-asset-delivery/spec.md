## ADDED Requirements

### Requirement: Immutable caching of hashed assets

The server SHALL serve a content-hashed file under `/web/assets/` with
`Cache-Control: public, max-age=31536000, immutable`, unless it rewrote that file's bytes for a
configured base path.

#### Scenario: Hashed bundle file

- **WHEN** a client requests an existing content-hashed file under `/web/assets/`
- **THEN** the response carries `Cache-Control: public, max-age=31536000, immutable`

#### Scenario: Missing asset

- **WHEN** a client requests a path under `/web/assets/` that the bundle does not contain
- **THEN** the response is `404` and carries no `Cache-Control` header

#### Scenario: Unhashed file

- **WHEN** a client requests the shell, `sw.js` or another file without a content hash
- **THEN** the response's caching headers are unchanged by this requirement

### Requirement: Compressed text assets

The server SHALL serve embedded text assets gzip-encoded with `Vary: Accept-Encoding` when the
request accepts gzip and has no `Range` header, and SHALL compress each file at most once per
process.

#### Scenario: Client accepts gzip

- **WHEN** a request for a JS or CSS asset carries `Accept-Encoding: gzip`
- **THEN** the response carries `Content-Encoding: gzip` and its body decodes to the file's bytes

#### Scenario: Client does not accept gzip

- **WHEN** a request carries no `Accept-Encoding` header or gives gzip a quality of zero
- **THEN** the response carries no `Content-Encoding` header and its body is the file's bytes

#### Scenario: Range request

- **WHEN** a request for a text asset carries a `Range` header
- **THEN** the response is not gzip-encoded

### Requirement: Service worker caches fetched assets

The service worker SHALL store a same-origin `/web/assets/` response with status `200` in its
versioned cache when it fetches one, and SHALL serve it from that cache afterwards.

#### Scenario: Lazy chunk fetched once

- **WHEN** the service worker fetches a `/web/assets/` file that is not in its cache and receives `200`
- **THEN** it stores the response and answers the next request for that file from the cache

#### Scenario: Failed or foreign response

- **WHEN** the fetched response is not `200`, is opaque, or is outside `/web/assets/`
- **THEN** the service worker does not store it
