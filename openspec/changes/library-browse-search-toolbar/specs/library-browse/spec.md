## ADDED Requirements

### Requirement: Library browse narrows by title text

`libraryItems` and `libraryEntities` SHALL accept an optional `query` string of at most 200
characters and keep only rows whose title or sort title contains every word of it, ignoring case,
accents and punctuation. A blank `query` SHALL keep every row. Facets SHALL describe the library
without the text filter.

#### Scenario: Words in any order

- **WHEN** a client requests `libraryItems` with `query: "bunny BIG"` and the library holds an item
  titled "Big Buck Bunny"
- **THEN** that item is in the response and `total` counts only matching items

#### Scenario: Too long

- **WHEN** a client requests `libraryItems` with a `query` longer than 200 characters
- **THEN** the read answers a client error and returns no items

### Requirement: Web browse toolbar is one row with a Filters panel

The web Library browse view SHALL show one toolbar row holding a labelled search box, the view
switcher, a Filters button showing the number of active filters, one Sort control for field and
direction, the poster size and the display choice. The filters SHALL open in a popover on wide
screens and a bottom sheet on phones.

#### Scenario: Filters panel

- **WHEN** a viewer activates the Filters button
- **THEN** the panel opens, the button reports it is expanded, and Escape closes it and returns
  focus to the button

### Requirement: Web browse search and active filter chips

The web Library search text SHALL be kept in the URL as `q` and sent to the server after the viewer
stops typing; the search box SHALL have its own clear button. Active filters SHALL show as chips
named "Remove <filter>", with a Clear all control that clears the filters and the search.

#### Scenario: Search narrows the library

- **WHEN** a viewer types a word into the library search
- **THEN** the URL carries `q`, the count and grid show only matching items, and the search box's
  clear button removes it

#### Scenario: Search matches nothing

- **WHEN** a viewer searches for text no title holds
- **THEN** the view says nothing matches the search and offers to clear it
