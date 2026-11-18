# Categories pivot that leads with genres and tames the long tail

Ticket: CORE-2505 (`queue:core:main`).

## Why

Tower QA 2026-11-18: the web Library Categories pivot (`/n/:nodeId/categories`) renders every value
of every grouping as the same blank text tile. The Films library shows 200 studios (the facet cap,
cut alphabetically at "Image Entertainment") ahead of its 19 genres, with no artwork, no counts and
no ordering by size. The owner: "the categories tab needs reworking. its pretty overwhelming and
needs rethinking". Plex and Jellyfin lead with genres and keep studios, networks and countries
behind a short list and a search.

The server reports grouping values without counts, and its 200-value cap keeps the alphabetically
first titles, so a client can neither rank a long tail nor reach "Warner Bros." in it.

## What changes

- **Server (additive wire minor).** `libraryItems.facets.groupings[]` gains `counts`, parallel to
  `values`: `counts[i]` is how many distinct visible Media reach `values[i]`, computed from the rows
  the facet already reads. When a kind has more values than the facet cap, the cap keeps the most
  used values (ties by title) instead of the alphabetically first; `values` stays in title order.
  The client-core wire contract publishes a new additive minor.
- **Web Categories pivot.** The pivot is redesigned around grouping size, with no kind named by the
  client:
  - A *featured* grouping (at most 40 values, such as genres) leads, as a grid of landscape artwork
    tiles: each tile shows the value, its item count and a backdrop taken from that value's
    best-rated item (one `libraryItems` read per value, four at a time, a tile never repeating an
    item an earlier tile shows). A tile without artwork keeps the accent surface.
  - A *long-tail* grouping (more than 40 values, such as studios) shows only its top 12 values by
    count, leaving out values with a single item, then a "See all" link to a searchable A-Z list of
    every value at `?kind=<kind>` on the same pivot, grouped by initial with a letter row and a
    filter field. Without counts (an older server) it shows no top list, only "See all".
  - With two or more groupings, a row of jump links under the pivots scrolls to each grouping.
  - Groupings with no values stay hidden; no groupings keeps the empty state.
- Styles reuse the viewer tokens (`--library-accent`, `--scrim`, `--on-scrim`, radii, focus ring)
  and add no motion beyond the existing hover/focus transitions; no visual effect code is added
  (effects belong to Oscillux).

## Non-goals

- Native clients (follow-up per client once the wire minor is recorded).
- Server-side artwork per grouping value: the tiles reuse the existing `libraryItems` read.
- Changing the Library browse filter bar, which keeps reading `values`.

## Failure modes

- A failed facet read shows the existing retry alert.
- A failed per-value artwork read leaves that tile on the accent surface; its count still shows.
- An unknown `?kind=` falls back to the overview.
