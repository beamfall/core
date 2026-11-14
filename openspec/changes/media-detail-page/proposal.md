# Media items open a detail page

Ticket: CORE-1144 (`queue:core:main`).

## Why

On the web client, clicking any Media card starts playback at once. The viewer cannot read an
item's summary, see its declared fields or decide between playing and resuming before the player
opens, and a misclick on a shelf or grid starts a stream. Entities already have a detail page
(`/entity/:id`); a single Media item has none.

## What Changes

- The web viewer gains a route for one Media item, `/media/:mediaId`, drawn by a new
  `MediaDetailPage`.
- The page header shows the item's artwork (from the card that opened it, when the card carried
  artwork slots), its title, its year, its summary and the plugin-declared fields from the existing
  `mediaFields` query, labelled with each field's display label. Core names no field and adds no
  domain word.
- One primary action plays the item. It reads "Resume" with a progress bar when the card that
  opened the page carried a known position and duration, and "Play" otherwise. The player keeps
  resuming from the server timeline as it does today.
- A back control returns to the previous page; scroll position on that page is restored by the
  existing viewer scroll restoration.
- The page paints at once from the opening card or the viewer cache, shows a skeleton while it has
  nothing to paint, a generic "not available" state when the item is unknown or hidden (no oracle),
  and a retryable error state when the request fails.
- When the item came from a section still in the viewer cache, the page shows the section's other
  items as a "more from" shelf, from cache only (no extra request).
- Card activation changes deliberately:
  - a Media poster card in a shelf or grid section opens the detail page;
  - a Media row in a list section keeps playing at once;
  - the hero's Play button keeps playing at once;
  - entity cards keep opening the entity page.
- Strings ship in en, fr, de, es and pt.

## Wire compatibility

No server, schema or wire-contract change. The page reads the existing `media(id)` and
`mediaFields(mediaID)` queries. Artwork and resume progress come only from what the opening card
already carried, because `media(id)` returns neither; a deep link to `/media/:id` therefore shows a
title-led header and a plain "Play". Adding artwork slots and the viewer's position to `media(id)`
is a follow-up that changes the reflected schema and the wire goldens.

## Non-goals

- A "start over" action: the player resumes from the server timeline even with `?t=0`, so it needs
  a player change first.
- Related items through plugin relations: relation names are not in the manifest.
- A plugin-declared Media detail Layout; detail Layouts exist only for entity kinds.
- Native clients.

## Capabilities

### New Capabilities

- `media-detail-page`: the web viewer shows one Media item on its own page and plays it from there.

### Modified Capabilities

None.
