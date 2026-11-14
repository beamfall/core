# The `/v1` Media list carries year and duration

Ticket: CORE-1160 (`queue:core:main`).

## Why

`GET /v1/libraries/{libraryID}/media` returns each Media with only its ID, Library, title and
summary. The published contract (`docs/api/openapi.json`, `v1MediaItem`) already declares `year`
and `durationSeconds` on that item, and `GET /v1/media/{mediaID}` fills them, so an integration that
lists a Library has to make one detail request per item to learn two fields the list promises.

## What Changes

- Each item in the list carries `year` and `durationSeconds` when the server knows them, with the
  same values the detail read returns. An unknown value is omitted, as it is on the detail read.
- The page is filled with one extra reveal-filtered read for the whole page, not one per item.

## Wire compatibility

Additive and already declared: both fields are optional members of `v1MediaItem` in the published
contract. No contract artifact changes.

## Capabilities

### New Capabilities

- `v1-catalog-list`: what a `/v1` Media list item carries.

### Modified Capabilities

None.
