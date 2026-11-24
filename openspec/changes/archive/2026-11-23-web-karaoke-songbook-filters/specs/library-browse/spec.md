## ADDED Requirements

### Requirement: Library browse filters and facets by Filterable text Media fields

`libraryItems` SHALL accept `fieldFilters`, mapping a text Media field the Library's plugin declares
Filterable to one value, and keep only the items holding every named value exactly. It SHALL accept
`facetFields`, naming such fields, and report under `facets.fields` each field's values in the
Library with their item counts, most items first, counted before any search or filter, at most 50
values per field. Any other field, shape or entry SHALL answer a client error.

#### Scenario: Duets only

- **WHEN** a client requests `libraryItems` with `fieldFilters: {singers: "duet"}` and
  `facetFields: ["singers"]` on a karaoke Library
- **THEN** only duets come back, and `facets.fields` reports `singers` with the value `duet` and
  its count over the whole Library

#### Scenario: Unfilterable field

- **WHEN** a client requests `libraryItems` with `fieldFilters: {"lyrics-sync": "word"}` and the
  plugin does not declare `lyrics-sync` Filterable
- **THEN** the read answers a client error and returns no items

#### Scenario: Sensitive or malformed

- **WHEN** a client names a field the plugin declares Sensitive, passes `fieldFilters` that is not
  an object or `facetFields` that is not a list, or sends a blank or padded entry or more than
  eight entries
- **THEN** the read answers a client error rather than ignoring the argument

### Requirement: The web songbook tracks the scroll position and filters by format

The songbook's letter rail SHALL mark the letter of the first song on screen. Chips SHALL filter
by the format, duet and guide-vocal values the karaoke Library has, each showing its song count,
and SHALL not appear for a value the Library lacks.

#### Scenario: Duets only

- **WHEN** the host chooses the Duet chip
- **THEN** the songbook lists only duets, and the rail and count follow the narrowed list

#### Scenario: Scrolled to B

- **WHEN** the host scrolls until the first song on screen is by an artist starting with B
- **THEN** the rail marks B
