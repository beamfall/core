## ADDED Requirements

### Requirement: Library browse sorts by a plugin-declared text Media field

`libraryItems` SHALL accept `sort: "field:<name>"` for a text Media field the Library's plugin
declares Sortable, ordering by its value ignoring case, then by title. Items with no value SHALL
come last in both directions, and the letter index SHALL be computed from the value, leaving them
out. Any other field sort SHALL answer a client error, as SHALL a field sort on `libraryEntities`.

#### Scenario: Ordered by artist

- **WHEN** a client requests `libraryItems` with `sort: "field:artist"` on a Library whose plugin
  declares `artist` as a Sortable text field
- **THEN** items come back ordered by artist, items without an artist come last, and `letters`
  report where each artist initial starts

#### Scenario: Undeclared field

- **WHEN** a client requests `libraryItems` with `sort: "field:tempo"` and the plugin does not
  declare `tempo` as a Sortable text field
- **THEN** the read answers a client error and returns no items

### Requirement: Library browse text matches Searchable Media fields

The `libraryItems` text filter SHALL also keep an item when every word of `query` appears in its
title together with the values of the text Media fields the Library's plugin declares Searchable,
ignoring case, accents and punctuation.

#### Scenario: Artist and title in one search

- **WHEN** a client requests `libraryItems` with `query: "abba waterloo"` and an item titled
  "Waterloo" has the Searchable field `artist` set to "ABBA"
- **THEN** that item is in the response

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

### Requirement: The web karaoke Library opens on a songbook and the party

On the web, a karaoke Library SHALL open on a Songbook pivot in place of Recommended and Library,
keeping Collections and Categories. The Songbook SHALL search artist and title together, group
songs under letter headers with the letter rail, and offer Play and Add for a singer on each song.
A party rail beside it SHALL start a party, or show the join code, stage, requests and rotation.

#### Scenario: Host adds a song for a singer

- **WHEN** a host searches the karaoke Library for an artist and chooses Add for a song
- **THEN** they pick a recent singer or type a new name, and the song joins the rotation for that
  singer without leaving the Library page

#### Scenario: No party yet

- **WHEN** the host opens the karaoke Library with no party running
- **THEN** the rail offers to start one over this Library

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
