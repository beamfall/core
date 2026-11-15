# developer-api-reference Specification

## Purpose
How a running Beamfall server publishes its developer API: the OpenAPI document the contract checks
validate and a browsable, script-free reference rendered from it.

## Requirements

### Requirement: A running server publishes its developer API reference

The server SHALL serve the developer API's OpenAPI document at `GET /api/docs/openapi.json` and a
browsable reference rendered from it at `GET /api/docs`, without a credential. The served document
SHALL be byte-identical to `docs/api/openapi.json`, the document the contract checks validate.

#### Scenario: Fetch the OpenAPI document

- **WHEN** a client requests `GET /api/docs/openapi.json`
- **THEN** the response is `200` with `application/json` and a body identical to
  `docs/api/openapi.json`

#### Scenario: Browse the reference

- **WHEN** a browser requests `GET /api/docs`
- **THEN** the page lists every operation in the document with its method, path and summary, links
  to the OpenAPI document, and carries no script
