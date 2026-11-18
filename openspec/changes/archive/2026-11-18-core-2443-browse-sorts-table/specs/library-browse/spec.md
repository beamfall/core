## ADDED Requirements

### Requirement: Library browse sorts by release date, audience score and plays

`libraryItems` and `libraryEntities` SHALL accept the sort keys `release-date` (the `release-date`
field, else 1 January of the year), `audience-score` (the 0 to 10 `community-rating` field) and
`plays` (the Profile's play count, unplayed counting 0). Items without the key SHALL sort last in
both directions. An Entity SHALL take the earliest release, highest score and total plays of the
visible Media it reaches.

#### Scenario: Newest release first

- **WHEN** a client asks for a Library sorted by `release-date` descending
- **THEN** Media order by release date, newest first, a Media with only a year sorts as 1 January
  of that year, and Media with neither come last

#### Scenario: Best audience score first

- **WHEN** a client asks for a Library sorted by `audience-score` descending
- **THEN** Media order by their audience score, highest first, and unscored Media come last

#### Scenario: Most played first

- **WHEN** a Profile asks for a Library sorted by `plays` descending
- **THEN** Media order by that Profile's play count, most played first, with unplayed Media last

### Requirement: Web Library browse offers a year range

The web Library browse SHALL offer a release-year range kept in the URL as `from` and `to`, sent as
`yearFrom` and `yearTo`. Setting a range SHALL clear the decade filter and setting a decade SHALL
clear the range. An active range SHALL show a chip that clears it.

#### Scenario: Shared year range

- **WHEN** a viewer opens `/n/:nodeId/all?from=1990&to=1999`
- **THEN** the browse reads Media released from 1990 to 1999 and shows a chip naming that range

### Requirement: Web Library browse offers a table view

The web Library browse SHALL offer a table view mode: dense rows without artwork that show each
item's title, year, runtime and audience score where it has them, remembered per Library with the
other view modes.

#### Scenario: Switch to the table view

- **WHEN** a viewer picks the table view
- **THEN** the Library's items render as dense rows with their year and runtime, and the choice is
  kept for that Library on the next visit
