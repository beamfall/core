## 1. Store

- [x] 1.1 `AddMediaExternalID` appends `media.updated` when it inserts a new identifier
- [x] 1.2 `MediaExternalIDsBatch` reads identifiers for many Media in bounded, chunked reads
- [x] 1.3 Tests: one event per new identifier; batch groups by Media

## 2. /v1

- [x] 2.1 `GET /v1/media?scheme=&value=` (`lookupMediaByExternalID`), visibility-filtered, at most 20 results
- [x] 2.2 `GET /v1/media/{mediaID}` carries `externalIDs` (at most 32)
- [x] 2.3 OpenAPI, generated reference and LLM docs, list bounds in the route contract
- [x] 2.4 Tests: scope and visibility filtering, bad queries, empty result, detail identifiers

## 3. Contract and docs

- [x] 3.1 Regenerate wire goldens; publish client-core-wire 2.5.0 and repoint the registry
- [x] 3.2 `docs/api/request-tools.md` documents the request-tool flow
