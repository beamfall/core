## ADDED Requirements

### Requirement: Web Library page offers pivot tabs

Every web Library page SHALL show, under the Library name, links to four pivots in this order:
Recommended (`/n/:nodeId`, the plugin's hub Layout), Library (`/n/:nodeId/all`), Collections
(`/n/:nodeId/collections`) and Categories (`/n/:nodeId/categories`). The open pivot SHALL carry
`aria-current="page"`, and each pivot SHALL be reachable directly by its address.

#### Scenario: Deep link to a pivot

- **WHEN** a viewer opens `/n/:nodeId/categories` for a Library node
- **THEN** the page shows the Library name, the four pivots with Categories marked current, and the
  Categories view

#### Scenario: Keyboard navigation

- **WHEN** a keyboard user tabs to the pivot row and activates the Collections link
- **THEN** the Collections pivot opens and its link is marked current

### Requirement: Collections pivot lists the Library's own Collections

The Collections pivot SHALL list only the Collections of the open Library, in curated order with
their item counts, each linking to its Collection page.

#### Scenario: Collections of another Library

- **WHEN** the household has Collections in Libraries A and B and the viewer opens A's Collections
  pivot
- **THEN** only A's Collections are listed

### Requirement: Categories pivot tiles each grouping value

The Categories pivot SHALL show one tile per value of each grouping the Library's plugin reports in
`libraryItems.facets.groupings`, grouped under the grouping's title. Activating a tile SHALL open
the Library pivot's Media view filtered to that grouping value.

#### Scenario: Open a category

- **WHEN** the plugin reports a grouping `genre` with value `Drama` and the viewer activates the
  `Drama` tile
- **THEN** the browser opens `/n/:nodeId/all?view=media&group.genre=Drama` and the grid lists only
  Media in that grouping value

#### Scenario: No groupings

- **WHEN** the Library's plugin reports no grouping
- **THEN** the Categories pivot shows an empty-state message
