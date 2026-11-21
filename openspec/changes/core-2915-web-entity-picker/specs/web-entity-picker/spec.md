## ADDED Requirements

### Requirement: Managers pick Media and containers by title

Every web Manage or Console field that identifies a Media item or an Entity SHALL offer a
search-and-pick control instead of a bare id text box. The control SHALL search by title through
the `searchResult` field, list each match with its poster, kind and Library, and give the page the
picked item's id. A field that accepts only Media or only Entities SHALL list only that kind.

#### Scenario: Pick by title

- **WHEN** a manager types part of a title into the field and chooses a listed match
- **THEN** the page receives that match's id and the field shows the match's title, kind and
  Library

#### Scenario: Paste an id

- **WHEN** a manager types or pastes text with no spaces and chooses "Use this ID"
- **THEN** the page receives that text as the id, as the old text box did

#### Scenario: Several items

- **WHEN** a field takes several ids
- **THEN** each pick adds one removable chip, "Use these IDs" splits pasted text on commas and
  spaces, and the page receives the chips' ids in order without duplicates

### Requirement: The picker is an accessible combobox with honest states

The control SHALL expose combobox semantics (labelled input, listbox, options) with arrow-key,
Enter and Escape handling from the shared Astryx primitive. While a search is in flight it SHALL
say it is searching; a query with no matches SHALL say so; a failed search SHALL say search is
unavailable without clearing the typed text.

#### Scenario: Search fails

- **WHEN** the search request fails
- **THEN** the list shows the unavailable message and "Use this ID" still works

### Requirement: A picked item links to where it is edited

A picked item SHALL show an Open link to its viewer detail page and, for Media, an Edit link to
its Manage item details page.

#### Scenario: Edit from a pick

- **WHEN** a manager picks a Media item on the Field locks page and follows Edit
- **THEN** Manage item details opens for that Media id
