# Library pivot tabs: Recommended, Library, Collections, Categories

Ticket: CORE-2437 (`queue:core:main`). Source: Plex web survey on the Tower Plex, 2026-11-17.

## Why

A web Library page (`/n/:nodeId`) shows the plugin's hub Layout with one button into the full
browse (`/n/:nodeId/all`). A Library's Collections are reachable only from the account menu, mixed
with every other Library's, and there is no way to browse a Library by its groupings (for example
the genres its plugin declares). Viewers coming from other media servers expect a row of pivots
under the Library name that moves between these views.

## What changes

- Every Library page shows a row of pivot tabs under the Library name: Recommended (the hub Layout,
  `/n/:nodeId`), Library (the full browse, `/n/:nodeId/all`), Collections
  (`/n/:nodeId/collections`) and Categories (`/n/:nodeId/categories`). Each pivot is a link with its
  own address, so it can be shared, bookmarked and reached with Back. The open pivot carries
  `aria-current="page"`.
- The Collections pivot lists only the Collections of that Library, in the same curated order and
  with the same item counts as the household Collections page, each opening the existing
  Collection page.
- The Categories pivot shows one tile per value of each grouping the Library's plugin declares
  (the `libraryItems` facets, before any filter). A tile opens the Library pivot's Media view
  filtered to that value (`/n/:nodeId/all?view=media&group.<kind>=<value>`).
- Web only: no Graph schema or wire change. Categories reuses `libraryItems.facets.groupings`;
  Collections reuses the `collections` read.

## Non-goals

- No hub changes on the Recommended pivot (its Layout is the plugin's).
- No Collection editing on the pivot; the household Collections page keeps creation.
- No grouping artwork on Category tiles.

## Failure modes

- A Library whose plugin declares no grouping shows an empty-state message on Categories.
- A Library with no Collections shows an empty-state message on Collections.
- A read failure on either pivot shows an alert with Retry.
