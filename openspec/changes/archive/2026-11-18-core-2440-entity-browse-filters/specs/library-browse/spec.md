## ADDED Requirements

### Requirement: Entity-kind browse sorts and filters by reached Media

`libraryEntities` SHALL accept the `libraryItems` browse arguments (`sort`, `direction`, `seed`,
`watch`, `groupFilters`, `yearFrom`, `yearTo`, `quality`, `minRating`) with the same validation.
It SHALL judge each listed Entity by the visible Media linked to it or to its child Entities: the
earliest year, the latest added, the total duration, the latest watched and the highest Profile
rating. `total` SHALL count the filtered Entities while kind totals stay unfiltered.

#### Scenario: Sort an Entity kind by year

- **WHEN** a client asks for an Entity kind sorted by `year` descending
- **THEN** Entities order by the earliest year of the Media they reach, newest first, and Entities
  with no year come last

#### Scenario: Unwatched Entities

- **WHEN** a client asks for an Entity kind with `watch: "unwatched"`
- **THEN** only Entities that reach at least one Media the Profile has not watched are listed

#### Scenario: Group filter on an Entity kind

- **WHEN** a client filters an Entity kind by a grouping value
- **THEN** an Entity linked to that value, or reaching Media linked to it, is listed, and others are
  not

#### Scenario: Invalid argument

- **WHEN** a client sends an unknown sort to `libraryEntities`
- **THEN** the read fails with a client error

### Requirement: Entity-kind browse reports a letter index

For a title sort, `libraryEntities` SHALL answer `letters`, the offset and count of each title
initial in the filtered set (`#` for titles that do not open with a letter); any other sort SHALL
answer an empty list.

#### Scenario: Jump to a letter

- **WHEN** a client requests the offset a letter reports
- **THEN** the first Entity of the page opens with that initial

### Requirement: Web Entity-kind view offers the browse toolbar and letter rail

The web Entity-kind view SHALL offer the Media view's sort, direction and filter controls and its
letter rail, keeping their state in the URL with the same keys as the Media view.

#### Scenario: Shared filtered address

- **WHEN** a viewer opens `/n/:nodeId/all?view=<kind>&sort=year&dir=desc&watch=unwatched`
- **THEN** the Entity-kind view opens sorted by year, newest first, showing only unwatched Entities
