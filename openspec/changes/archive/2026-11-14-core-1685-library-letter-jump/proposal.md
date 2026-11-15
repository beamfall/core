# Jump to a letter in a title-sorted library browse

Ticket: CORE-1685 (`queue:core:main`).

## Why

A title-sorted library of thousands of items can only be crossed by scrolling (web) or by repeated
Load more (native). Reaching titles that start with "S" means loading or scrolling past every page
before them. Comparable media servers offer an A-Z bar on web, TV and phone clients.

A client cannot build that bar itself: it would need every page to know where each letter starts.
The server already holds the whole filtered, sorted set while answering `libraryItems`, so it can
report where each initial starts at no extra read.

## What Changes

- `libraryItems` gains `letters: [{ letter, offset, count }]` (wire contract 2.5.0). When the order
  is `title`, each entry names one title initial, the offset of the first item with that initial in
  the filtered, sorted set and how many items carry it, in the order the items appear (so a
  descending sort lists Z first). The initial is the title's
  first character upper-cased when it is a letter, and `#` for anything else (digits,
  punctuation, an empty title). Any other sort answers an empty list.
- The field is additive and domain-agnostic: it reads only `Media.title`, the same key the title
  sort uses, so the offsets always match the order a client pages through.
- Web browse shows a letter rail beside a title-sorted grid or list of more than 60 items, after the
  approved console-list-controls board C (CORE-1068). `#` and A-Z are always shown; a letter with
  no items stays in place, disabled and labelled "X, no titles", and an enabled one reads
  "M, 64 titles". The letter at the top of the grid is highlighted as it scrolls. The rail is one
  tab stop: arrow keys, Home and End move along enabled letters and scroll the grid there; typing a
  letter jumps to it or to the next letter with titles, and says which; Enter or a click also
  focuses the first item. Dragging shows a bubble with the letter and its count and jumps on
  release. Below 360px wide or 520px tall a "Jump to" control opens a sheet of letters instead.
  A jump scrolls the virtualized grid to the offset and loads only the pages in view.
- Board C also asks the rail to ignore leading articles ("The", "A", "An"). Core has no sort-title
  concept and must not carry a language's article list, and the rail must agree with the order, so
  the initials use the raw title key. A plugin-supplied sort title is CORE-1768.
- Native clients consume the same field. Their browse does not yet use `libraryItems` (CORE-1220),
  so their rails are child tickets: CORE-1767 (iPhone, tvOS focusable) and CORE-1769 (Android phone,
  TV focusable).

## Impact

- Wire: one optional response field on `libraryItems`, published as wire contract 2.5.0 (wire goldens
  regenerated). Consumers re-vendor 2.5.0 before the workspace registry pins it.
- Store: letter offsets computed after sorting in `LibraryBrowse`.
- Web: `LibraryLetterRail` component, a grid index helper in `useVirtualGrid` and the jump in the
  browse page; new strings in the catalog.
