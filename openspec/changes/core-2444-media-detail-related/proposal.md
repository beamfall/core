# Related and "More with" rows on the web Media detail page

Ticket: CORE-2444 (`queue:core:main`).

## Why

Plex ends a title's page with a row of related titles and a row headed with the lead person
("More with ..."). The web Media detail page ends with the cast and the Keep browsing row, so the
viewer has to go back to find something like what they just opened.

## What changes

- The web Media detail page shows a "More with <name>" row: the other Media credited to the item's
  first billed person (its first person-archetype credit), read with `mediaForEntity`.
- It shows a Related row: Media that share at least one category-archetype credit with the item
  (genres and the like), ranked by how many of the item's category credits and first three billed
  people they share.
- Both rows exclude the current item and are hidden when empty or when a read fails. Their cards
  open the item's detail page.

## Non-goals

- A server `related` source kind declared by a plugin layout. Source kinds are the plugin SDK's
  fixed vocabulary, so a declarable kind needs an SDK release and plugin layout changes first
  (follow-up). Until then the web ranks with existing reads, at most seven `mediaForEntity` calls
  per page.
- The rows on a container Entity's page (follow-up) and on native clients.

## Failure modes

- A failed `mediaForEntity` read drops that credit's contribution; when every read fails the rows
  are hidden. The rest of the page is unaffected.
- Media outside the session's Libraries or above its reveal level are omitted by the server, as
  before.
