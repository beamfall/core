# web-viewer-search Specification

## Purpose
Let a viewer search from a box in the web viewer header, with grouped results shown as they type, keyboard actions to open or play each result, and a full search page whose query lives in the URL.

## Requirements

### Requirement: Search is reachable from every viewer page

The web viewer SHALL render a search field in the header of every viewer page, and SHALL move focus
to it when the user presses `/` outside an editable element or Cmd/Ctrl-K anywhere.

#### Scenario: Shortcut focuses the field

- **WHEN** a viewer page has focus outside any editable element and the user presses `/`
- **THEN** the header search field receives focus and no `/` is typed into it

#### Scenario: Shortcut is ignored while typing

- **WHEN** focus is in a text input and the user presses `/`
- **THEN** the character is typed into that input and the search field does not take focus

### Requirement: Results update as the user types

The header search field and the search page SHALL request results after a short debounce as the
query changes, and SHALL cancel the in-flight request for a superseded query so that an older reply
never replaces a newer one.

#### Scenario: A newer query supersedes an older one

- **WHEN** the user types a query, and types more before the first reply arrives
- **THEN** the first request is aborted and only results for the latest query are shown

### Requirement: Result rows are grouped and descriptive

The header search results SHALL be grouped by type, and each row SHALL show artwork, title, year
when known, type and Library title when known.

#### Scenario: Mixed results

- **WHEN** a query matches Media and Entities
- **THEN** rows appear under one heading per type, each with its title, type and Library

### Requirement: Results are keyboard operable

The header search SHALL expose the combobox pattern: Up/Down move the active row, Enter opens the
active row's page, Escape closes the list, and Media rows SHALL offer a separate Play control that
starts playback.

#### Scenario: Open versus play

- **WHEN** the user presses Enter on an active Entity row
- **THEN** that Entity's page opens; a Media row opens the Media's own page, and its Play control
  starts playback directly

#### Scenario: Enter with no active row

- **WHEN** the user presses Enter with a query and no active row
- **THEN** the full search page opens for that query

### Requirement: Empty and no-result states name the query

When a query has no results, the web viewer SHALL say what was searched and offer a next step. The
search page SHALL NOT show a results heading before a query exists.

#### Scenario: No results on the search page

- **WHEN** the search page has a query with no results
- **THEN** it names the query and offers a Clear search action that empties the query and the URL

### Requirement: Result counts are announced

The web viewer SHALL announce the number of results for the current query, or that there are none,
through a polite live region.

#### Scenario: Results arrive

- **WHEN** results for a query are shown
- **THEN** the live region text states the result count and the query

### Requirement: The search page query lives in the URL

The search page SHALL read its initial query from the `q` URL parameter and keep the parameter in
step with the field.

#### Scenario: Reload keeps the search

- **WHEN** the user reloads `/search?q=space`
- **THEN** the field holds `space` and its results are shown

### Requirement: Search rows carry library, year and type

`searchResult` rows SHALL carry optional `libraryID`, `libraryTitle`, `year`, `mediaClass` and
`entityKind` / `entityKindDisplayName` fields, filled only for rows whose Library is visible to the
caller's scope.

#### Scenario: Visible Library

- **WHEN** a keyword search returns a Media row in a Library visible to the caller
- **THEN** the row carries that Library's ID and title, the Media class, and the year when set

#### Scenario: Unresolved Library

- **WHEN** a row's Library is not visible to the caller's scope
- **THEN** the row carries none of the optional fields
