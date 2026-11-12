## ADDED Requirements

### Requirement: Library browse read reports the whole filtered set

The Graph read `libraryItems` SHALL return one page of a library's Media together with `total`, the
number of items matching the request's filters across all pages. It SHALL apply the same visibility
as the all-items Source: reveal state, library allow-list, certification ceiling, trash, extras and
per-Profile Mute.

#### Scenario: Paging keeps the total

- **WHEN** a client requests `libraryItems` with `limit: 1, offset: 1` for a library of N visible
  items
- **THEN** the response holds one item, the second in the requested order, and `total` is N

#### Scenario: Library outside the session scope

- **WHEN** a session that has not revealed a restricted library requests its `libraryItems`
- **THEN** `total` is 0, `items` is empty and every facet list is empty

### Requirement: Library browse sorts server-side

`libraryItems` SHALL order the filtered set by `sort` (`title`, `year`, `added`, `duration`,
`last-watched` or `random`) in `direction` (`asc` or `desc`) before paging. Items that lack the sort
key SHALL come last in both directions. `random` SHALL return the same order for the same `seed`.

#### Scenario: Reversed direction

- **WHEN** a client requests `sort: "title"` once with `direction: "asc"` and once with `"desc"`
- **THEN** the second response lists the same set in reverse title order

#### Scenario: Unknown sort

- **WHEN** a client requests a `sort` the server does not support
- **THEN** the read answers a client error and returns no items

### Requirement: Library browse filters by watch state, entity, year and quality

`libraryItems` SHALL keep only items matching every supplied filter: `watch` (`unwatched`,
`in-progress`, `watched`, evaluated for the calling Profile), `entityFilter` (items related to the
named entity), `yearFrom`/`yearTo` (inclusive bounds) and `quality` (a resolution tier). `facets`
SHALL list the entity titles of `facetEntityKind`, the decades and the quality tiers present in the
library's visible set before filtering.

#### Scenario: In-progress filter

- **WHEN** the calling Profile has a resume position on one item and requests `watch: "in-progress"`
- **THEN** the response contains that item and no item the Profile has finished or never started

#### Scenario: Unknown filter value

- **WHEN** a client supplies a `watch` or `quality` value the server does not support
- **THEN** the read answers a client error

### Requirement: Web library page offers a full browse view

The web viewer SHALL offer, for each library, a browse view that shows the total item count, renders
the set in a virtualized grid or list, and keeps the sort and every active filter in the URL. Active
filters SHALL be shown as removable controls. The grid or list choice SHALL be remembered per
library. Every control SHALL be keyboard operable and have an accessible name.

#### Scenario: Shared URL restores the view

- **WHEN** a user opens a browse URL carrying a sort and a filter
- **THEN** the view loads with that sort and filter applied and shown as active

#### Scenario: Removing a filter

- **WHEN** a user activates an active filter's remove control
- **THEN** the filter leaves the URL and the count and items update to the wider set

### Requirement: Play all and Shuffle play the current filtered set

The browse view SHALL offer Play all and Shuffle. Play all SHALL start playback at the first item of
the current filtered set in the current order and continue through the set. Shuffle SHALL play the
same set in a random order.

#### Scenario: Play all with a filter active

- **WHEN** a user activates Play all while a filter is active
- **THEN** playback starts on the first filtered item and the next item played is the second
  filtered item, never an item the filter excludes
