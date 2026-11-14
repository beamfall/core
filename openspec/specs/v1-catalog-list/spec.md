# v1-catalog-list Specification

## Purpose
Defines the v1 Media list wire shape, including the optional year and duration fields clients sort and display by.

## Requirements

### Requirement: Media list items carry year and duration

Each item returned by `GET /v1/libraries/{libraryID}/media` SHALL carry `year` and
`durationSeconds` when the server knows them, with the same values `GET /v1/media/{mediaID}` returns
for that Media. A value the server does not know SHALL be omitted. The server SHALL read these
values for a page with a bounded number of store reads that does not grow with the page size, and
SHALL apply the same visibility filter as the list itself.

#### Scenario: Known values

- **WHEN** a client lists a Library whose Media has a known year and duration
- **THEN** the item carries `year` and `durationSeconds` equal to the detail read's values

#### Scenario: Unknown value

- **WHEN** a client lists a Library whose Media has no known year
- **THEN** the item has no `year` member
