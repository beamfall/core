# Library search and a one-row browse toolbar

Ticket: CORE-2568 (`queue:core:main`), web first. Phone sheet shares the component with CORE-1787.

## Why

The web Library page stacks the All items/Folders chips, nine sort and filter controls across three
rows, the poster size chips and the display chips above the grid. Viewers asked for a search inside
the library and a calmer toolbar. Comparable media servers keep one row: search, a Filters button,
sort and display, with the filters themselves in a popover.

## What Changes

- `libraryItems` and `libraryEntities` take an optional `query` string (at most 200 characters).
  It keeps only rows whose title or sort title holds every word of it, ignoring case, accents and
  punctuation. Facets still describe the unfiltered library. Additive: client-core-wire 2.34.0.
- The web browse toolbar becomes one row: a search box (debounced, kept in the URL as `q`, with a
  label and a clear button), the view switcher (All items/Folders/Entity kinds), a Filters button
  with the active count, one Sort control holding field and direction, the poster size and the
  display choice.
- The watch, rating, grouping, decade, year and quality filters move into a Filters panel: a
  popover on wide screens and a bottom sheet on phones.
- Active filters show as removable chips under the row, with Clear all for filters and search; the
  search box has its own clear button.
- A search that matches nothing shows its own empty state.

## Non-goals

- No change to the global search page or its index.
- Multi-select filters (CORE-1219) are a separate change.

## Impact

`internal/graph/library_browse.go`, `nested_schema.go`, `internal/store/library_browse.go`,
`library_entities.go`, wire goldens and `contracts/registry.json`;
`internal/web/app/src/areas/viewer` browse toolbar, `lib/libraryBrowse.ts`, strings.
