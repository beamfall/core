## ADDED Requirements

### Requirement: Viewers read their own watch history and stats

The session fields `myPlayHistory` and `myPlayStats` SHALL return the same plays and breakdowns as
`playHistory` and `playStats`, limited to the signed-in profile, with no profile argument. They
SHALL drop plays of items the profile cannot see. A session with no profile SHALL be refused.

#### Scenario: Viewer sees only their own plays

- **WHEN** two profiles have plays and one of them reads `myPlayHistory`
- **THEN** only that profile's plays are returned

#### Scenario: Hidden item

- **WHEN** a profile played an item in a Library it can no longer see
- **THEN** `myPlayHistory` does not list that play and `myPlayStats` does not count it
