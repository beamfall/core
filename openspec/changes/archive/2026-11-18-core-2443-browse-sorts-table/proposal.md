# More Library browse sorts, a year range and a dense table view

Ticket: CORE-2443 (`queue:core:main`). Source: Plex web survey on the Tower Plex, 2026-11-17.

## Why

The Library browse (`libraryItems`, and `libraryEntities` since CORE-2440) offers six sorts: title,
year, added, duration, last watched and the Profile's rating. Common browses are missing: newest
release first, the best audience score, the most played. The only year filter is a whole decade,
although the server already takes `yearFrom` and `yearTo`. The views are poster, thumbnail and list
rows; there is no dense view that shows many titles with their year, runtime and score at once.

## What changes

- Three sort keys, accepted by `libraryItems` and `libraryEntities` (additive enum values):
  - `release-date`: the Media's core-conventional `release-date` date field, or 1 January of its
    year when it has none.
  - `audience-score`: the core-conventional `community-rating` number field (0 to 10), the score
    the browse already reads for cards.
  - `plays`: the requesting Profile's play count; an unplayed item counts 0 plays and orders as
    the fewest, and without a Profile the sort falls back to the title order.
  - Items missing the key trail in both directions, as on the existing sorts. An Entity takes the
    earliest release, the highest score and the total plays of the Media it reaches.
- Web:
  - The sort menu offers the three keys (plays only where viewers watch, as for last watched).
  - A year range (`from`/`to` in the URL) narrows by release year. It replaces the decade when set,
    and the decade replaces it; its chip names and clears it.
  - A table view mode: dense rows without artwork, showing the title, year, runtime and audience
    score, kept per Library like the other modes.

## Non-goals

- Codec, HDR, audio-format and language filters: they need per-stream facets the browse does not
  read yet, so they get their own ticket.
- Critic scores: no core-conventional field carries one.
- An "Add to" action on the browse page: cards already offer it from their More menu.

## Failure modes

- An unknown sort is a client error, as before.
- A year range with `from` after `to` is swapped on read; out-of-range years are dropped.
