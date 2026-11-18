# Extras and Editions rows on the web Media detail page

Ticket: CORE-2439 (`queue:core:main`).

## Why

Scans link Extras to their parent Media with `extra-of`, and a manager can group cuts of one work
with `edition-of`. `mediaRelations` already returns both, but no viewer surface shows them: an
Extra is suppressed from browse, so on the web it cannot be reached at all. Plex and Jellyfin show
Extras and other editions as rows on the detail page.

## What changes

- `mediaRelations` items carry `artworkSlots`, `durationSeconds` and `mediaClass` for each related
  Media, the same values `media(id)` returns, so a client can draw a card. The fields already exist
  on `Media`; the schema is unchanged.
- The web Media detail page shows an Extras row (Media that are `extra-of` this item) and an
  Editions row (cuts that are `edition-of` this item) below the cast row. Each row is hidden when
  empty. Each card plays its item.

## Non-goals

- An Extra type label (trailer, featurette and so on). Scans store only the generic extra bit and
  the `extra-of` link, so there is no type to show. Showing it needs the scan or the plugin to store
  a type first (follow-up).
- Grouping editions in both directions. `mediaRelations` returns the cuts that point at this item,
  so the row is complete on the cut the others were linked to. Listing the whole group from every
  cut needs a forward read (follow-up).
- Native clients.

## Failure modes

- A failed relations read hides the rows; the rest of the page is unaffected.
- Related Media outside the session's Libraries are omitted, as before.
