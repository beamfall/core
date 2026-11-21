# Rethink Manage › Curation as "Versions & links"

Ticket: CORE-2913 (`queue:core:main`).

## Why

The owner could not tell what Manage › Curation is for. The page asks for a raw subject ID and a
"Media"/"Entity" subject type, then shows a field table that repeats Item metadata and Entities, a
free-text relation editor keyed by raw IDs, and a files/extras panel. Nothing on it says what job it
does or when to use it.

It also links extras backwards. It calls `linkMediaRelation(open item, relation, typed ID)`, but the
store and the scanner record an extra as `from = extra, to = parent`, and `mediaRelations` reads the
rows that point *at* the open item. A link made on this page never shows up under that item.

## What Changes

- Rename the page "Versions & links" in the nav and heading, with a lead line that names its three
  jobs: see the files behind an item, link its extras and alternate editions, and set the order of
  the items in a group. The `/manage/curation` route is unchanged.
- Find the subject by catalog search instead of a raw ID and type picker. An Entity result opens
  the group view; a Media result opens the item view. The open subject is kept in the URL
  (`?open=<id>&as=media|entity`) so a reload or a link reopens it.
- Remove the fields table (Item metadata and Entities already show and edit fields); an item links
  to its Item metadata page instead.
- Item view: separate "Files", "Extras" and "Alternate editions" sections, each with a one-line
  explanation and an empty state. Adding an extra or an edition picks the other item by search and
  writes `linkMediaRelation(picked, relation, open item)`, matching the scanner and the read.
- Group view: "Items in this group" in order, with move up/down, and "Add an item" picking the item
  by search and appending it at the end. The link type is chosen from the group's existing link
  types, or typed when the group has none.
- Sections that do not apply to the subject's type are not rendered.
- `upsertEntityRelation` keeps an existing row's attributes. The mutation has no attributes
  argument, so re-ordering a row the scanner wrote used to erase its attributes. The store upsert
  gains a `KeepAttributes` flag that the mutation sets; other writers are unchanged.
- `upsertEntityRelation` gains an optional `position` argument. The server places the row at that
  place among the rows the caller can see and renumbers the relation in one transaction, keeping
  hidden rows' order. The page makes one write per move or add instead of renumbering rows itself,
  so a failure cannot leave a half-applied order. The argument is optional, so no wire golden moves.
- `entityRelations` is read in pages (it already takes `limit`/`offset`), so ordering sees every row
  of a large group.

## Non-goals

- New fields or types: the one addition is the optional `position` argument above.
- Unlinking an extra, edition or group item: there is no mutation for it yet.
- Changing the Item metadata, Entities, Unmatched or Tags pages.
