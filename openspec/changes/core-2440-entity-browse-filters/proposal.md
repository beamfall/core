# Sort, filter and letter rail for Entity-kind Library views

Ticket: CORE-2440 (`queue:core:main`). Source: Plex web survey on the Tower Plex, 2026-11-17.

## Why

The web Library pivot (`/n/:nodeId/all`) switches between the Entity kinds a Library's plugin
declares (CORE-2362) and its Media grid. Only the Media grid can be sorted and filtered and has
the A to Z letter rail (`libraryItems`). An Entity-kind view (`libraryEntities`) takes only kind,
limit and offset, so it is always in title order with no filters and no way to jump to a letter.

## What changes

- `libraryEntities` accepts the same browse arguments as `libraryItems`: `sort`, `direction`,
  `seed`, `watch`, `groupFilters`, `yearFrom`, `yearTo`, `quality` and `minRating`, validated the
  same way, and answers `letters` for a title sort. The arguments are optional and additive.
- An Entity is judged by the visible Media it reaches: the Media linked to it, and the Media linked
  to its child Entities (the kinds that declare a relation to its kind). Its year is the earliest
  year of that Media, its added order the latest added, its duration the total, its last watched the
  latest and its rating the Profile's highest.
  - `unwatched` keeps an Entity with some unwatched Media, `in-progress` one with some Media in
    progress, and `watched` one whose Media are all watched.
  - A group filter keeps an Entity linked directly to the named grouping value or reaching Media
    linked to it.
  - Quality and rating filters keep an Entity that reaches some Media that matches.
- An Entity that reaches no visible Media still lists when no filter is set, and sorts after the
  others on any key it lacks. Kind totals stay unfiltered; `total` counts the filtered set.
- Web: an Entity-kind view shows the Media view's toolbar (sort, direction, watch state, grouping,
  decade, quality and rating filters, as the Library offers them) and its letter rail, with the
  state in the URL using the same keys.

## Non-goals

- No new sort keys (CORE-2443).
- No Entity-level ratings; the rating sort and filter read the Profile's Media ratings.
- No facets computed per Entity kind; the toolbar offers the Library's `libraryItems` facets.

## Failure modes

- An unknown sort, direction, watch state or quality, an invalid year range or rating, or a blank
  or excessive group filter is a client error, as on `libraryItems`.
- A filter that matches nothing answers an empty page with `total` 0 and the kind list intact.
