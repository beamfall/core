## ADDED Requirements

### Requirement: Rewritten assets revalidate

When the server rewrote a web asset's bytes for a configured base path, it SHALL serve that asset
with `Cache-Control: no-cache` and a strong `ETag` that identifies the served representation, and
SHALL answer a request whose `If-None-Match` matches that `ETag` with `304` and no body. The server
SHALL rewrite each asset at most once per process.

#### Scenario: First load under a base path

- **WHEN** a client requests a content-hashed text asset from a server configured with a base path
- **THEN** the response carries the rewritten bytes, `Cache-Control: no-cache` and a strong `ETag`

#### Scenario: Repeat load under a base path

- **WHEN** the client repeats the request with `If-None-Match` set to the `ETag` it received
- **THEN** the response is `304` with no body and the same `ETag`

#### Scenario: Validator of another representation

- **WHEN** a request for the identity representation carries the gzip representation's `ETag`
- **THEN** the response is `200` with the rewritten bytes
