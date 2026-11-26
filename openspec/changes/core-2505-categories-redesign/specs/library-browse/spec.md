## MODIFIED Requirements

### Requirement: Library browse reports the plugin's groupings

`facets` SHALL list the groupings, the decades and the quality tiers present in the library's
visible set before filtering. A grouping SHALL be a category-archetype entity kind declared by the
Library's plugin, reported with its kind, the plugin's display name, its values in title order and,
parallel to the values, `counts`: the number of distinct visible Media each value reaches.

#### Scenario: Capped and empty groupings

- **WHEN** a kind reaches more values than the facet cap
- **THEN** the reported values are the most used ones, ties broken by title
- **AND** a kind with no value in reach, and a Library without a plugin, report none

#### Scenario: Groupings come from the plugin

- **WHEN** a Library's plugin declares two category kinds and its visible items relate to entities
  of both
- **THEN** `facets.groupings` lists both kinds with the plugin's display names and their values
- **AND** a `groupFilters` entry for each kind returns only the items related to both entities

#### Scenario: Values carry their counts

- **WHEN** three visible Media relate to the genre `Drama` and one to `Western`
- **THEN** the genre grouping reports `values` `["Drama", "Western"]` and `counts` `[3, 1]`

#### Scenario: The cap keeps the most used values

- **WHEN** a kind reaches more values than the facet cap
- **THEN** the grouping reports the cap's worth of values with the highest counts, in title order

## REMOVED Requirements

### Requirement: Categories pivot tiles each grouping value

**Reason**: Replaced by "Categories pivot leads with featured groupings and shortens the long tail"
(CORE-2505): one equal tile per value buried genres under hundreds of studios.

**Migration**: Tiles still open the same filtered Media view.

## ADDED Requirements

### Requirement: Categories pivot leads with featured groupings

The Categories pivot SHALL show each grouping in `libraryItems.facets.groupings` that has values,
groupings of at most 40 values (featured) before larger ones (long tail), each in declaration
order. A featured grouping SHALL show every value as an artwork tile with its item count, the
artwork taken from an item in that value. Activating a value SHALL open the Library pivot's Media
view filtered to it.

#### Scenario: Open a category

- **WHEN** the plugin reports a grouping `genre` with value `Drama` and the viewer activates the
  `Drama` tile
- **THEN** the browser opens `/n/:nodeId/all?view=media&group.genre=Drama` and the grid lists only
  Media in that grouping value

#### Scenario: No groupings

- **WHEN** the Library's plugin reports no grouping, or none with a value
- **THEN** the Categories pivot shows an empty-state message

### Requirement: Categories pivot shortens a long-tail grouping

A long-tail grouping SHALL show at most its 12 values with the highest counts, leaving out values
with one item, followed by a "See all" link to the grouping's A-Z list.

#### Scenario: Genres lead, studios are shortened

- **WHEN** the plugin reports 19 genre values and 200 studio values, most studios with one item
- **THEN** the genre tiles come first with artwork and counts
- **AND** the studio section lists at most 12 studios, each with two or more items, by count, and
  a "See all" link

### Requirement: Categories pivot offers jump links

With two or more groupings shown, the Categories pivot SHALL offer a link to each grouping that
moves to it.

#### Scenario: Jump to studios

- **WHEN** the pivot shows a genre and a studio grouping and the viewer activates the Studio link
- **THEN** the Studio section scrolls into view and takes focus

### Requirement: Categories pivot lists every value of a grouping A-Z

The Categories pivot SHALL list every value of one grouping, in title order and grouped by
initial, at `/n/:nodeId/categories?kind=<kind>`, with each value's count when known, a filter field
that narrows the list as the viewer types, a row of initials that jumps to each group, and a link
back to the overview. An unknown kind SHALL show the overview.

#### Scenario: Filter the studios

- **WHEN** the viewer opens "See all" on the studio grouping and types `warner`
- **THEN** only studios whose name contains "warner", ignoring case and accents, stay listed
