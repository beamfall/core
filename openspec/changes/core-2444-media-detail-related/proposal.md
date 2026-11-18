# Related and "More with" rows on the web Media detail page

Ticket: CORE-2444 (`queue:core:main`).

## Why

Plex ends a title's page with a row of related titles and a row headed with the lead person
("More with ..."). The web Media detail page ends with the cast and the Keep browsing row, so the
viewer has to go back to find something like what they just opened.

## What changes

- The web Media detail page shows a "More with <name>" row: the other Media credited to the item's
  first billed person (its first person-archetype credit in the server's order), read through the
  `entity-media` Source with a limit of 20, so the Mute suppression and locked marking of every
  passive-discovery rail apply.
- A new additive graph read, `relatedMedia(mediaID, limit)`, returns Media that share at least one
  category-archetype credit with the item (genres and the like), ranked server-side by how many of
  the item's first four category credits and first three person credits (server order) they
  share. Each credit is read through the same `entity-media` Source, bounded per credit, so muted,
  hidden and locked Media are handled as on browse and Home. The limit defaults to 20 (at most 50).
  It is published in client-core-wire 2.26.0 with `mediaTracks`.
- The web shows that result as the Related row.
- Both rows exclude the current item and are hidden when empty or when a read fails. Their cards
  open the item's detail page.

## Non-goals

- A `related` Source kind declared by a plugin layout. Source kinds are the plugin SDK's fixed
  vocabulary, so a declarable kind needs an SDK release and plugin layout changes first
  (follow-up); `relatedMedia` is the server ranking such a kind would call.
- The rows on a container Entity's page (follow-up) and on native clients.

## Failure modes

- A failed read hides its row; the rest of the page is unaffected.
- A credit holding more Media than the per-credit bound contributes only its first Media in the
  Source's order, so the ranking is approximate for very large categories.
- Media outside the session's Libraries or above its reveal level are omitted by the server, as
  before.
