# Karaoke songbook: published field sort, scroll-tracked rail, format and duet chips

Tickets: CORE-3367, CORE-3368 and CORE-3369 (`queue:core:main`), follow-ups to CORE-3362
(`web-karaoke-songbook`).

## Why

The songbook shipped with a field sort the schema did not describe, a letter rail that marked the
window start rather than what is on screen, and no way to find duets or a given format.

## What changes

- The published `sort` argument and the agent catalog name the `field:<name>` form (CORE-3367).
  Behaviour is unchanged.
- `libraryItems` takes `fieldFilters` (a Filterable text Media field to one exact value, ANDed)
  and `facetFields` (Filterable text fields whose values and item counts come back under
  `facets.fields`), so a client offers only the formats and singer counts the Library has
  (CORE-3369). These change the wire contract: the landing stack publishes the next
  `client-core-wire` minor.
- On the web, the A-Z rail marks the letter of the first song on screen (CORE-3368), and chips
  filter by format (CDG, LRC, video, UltraStar), duet and guide vocal when the Library has them
  (CORE-3369).
