## ADDED Requirements

### Requirement: Play history can be searched and filtered on the server

The `recently-played` and `most-played` Source kinds SHALL accept a search text, a Library and
last-played bounds, and SHALL apply them before paging.

#### Scenario: Searching by artist

- **WHEN** a Profile requests `recently-played` with `query` set to an artist's name
- **THEN** only played Media linked to an Entity with that name, or titled with it, are returned

#### Scenario: Narrowing by last play

- **WHEN** the request sets `playedAfter` and `playedBefore`
- **THEN** only Media last played at or after `playedAfter` and before `playedBefore` are returned

#### Scenario: Paging a narrowed feed

- **WHEN** a narrowed feed is read with an offset
- **THEN** the offset counts matching Media only, so the page is full until the feed ends

#### Scenario: Malformed bound

- **WHEN** `playedAfter` or `playedBefore` is not an RFC 3339 time
- **THEN** the request fails with a client error

### Requirement: The History page offers search and filters

The web History page SHALL offer a search box, a Library filter and a last-played range, and both of
its feeds SHALL follow them.

#### Scenario: Filtering History

- **WHEN** the viewer types a search, picks a Library and picks "Past 7 days"
- **THEN** the recently played and most played feeds show only matching plays
