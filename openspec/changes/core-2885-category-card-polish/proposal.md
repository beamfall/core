# Category pivot cards: designed fallback art, singular count labels and a denser grid

Ticket: CORE-2885 (`queue:core:main`).

## Why

Owner screenshot of Music > Categories on the live server: a card whose value has no artwork is a
blank grey 16:9 rectangle, a value with one container reads "1 Albums", and the cards are large
and sparse ("pretty ugly"). Genre sourcing is about to list dozens of values per Library, most of
them without a backdrop, so the featured grid has to look designed with 20 to 100 cards and little
artwork.

## What changes

- **Designed fallback.** A featured card with no backdrop shows, in order of preference:
  - a cover mosaic of up to four distinct container covers taken from the items the card already
    reads for its backdrop (one, two or four tiles), under the existing label scrim;
  - otherwise a tinted surface: a gradient from the Library accent mixed with one of the theme
    palette tones, picked deterministically from the value's name, with the name set large over
    it in the theme's primary text colour. No new reads, no motion and no effect code.
- **Singular counts.** When a count is one in the display locale's plural rules, the container
  unit is named by the plugin's singular display name for that kind (`kindDisplayName`), read from
  `libraryEntities.kinds`, whose `label` is the same browse Section title the server sends in
  `browseLabels`. With no singular match the card falls back to the generic item count ("1 item"),
  never a plural noun. No wire change: both fields are already on the wire. No domain word is
  written in client code.
- **Denser grid.** Cards keep one 3:2 shape whatever their content; the grid fills columns of at
  least 168px with 12px gaps (two columns with 8px gaps on a phone), and the count is secondary
  text. Hover and focus keep the existing card border, shadow and focus ring.

## Non-goals

- Changing which groupings are featured (`FEATURED_MAX`), or the long-tail chip list.
- A server-side singular label (`browseLabels` stays plural); native clients.

## Failure modes

- A failed `libraryEntities` read leaves singular labels out (one-counts show the item count); the
  page still renders.
- A cover that fails to load drops out of the mosaic; with none left the card shows the tinted
  surface.
