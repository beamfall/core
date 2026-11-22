# Media detail lists a numbered container as numbered rows

Ticket: CORE-3065 (`queue:core:main`).

## Why

An item in an audio container (one track of an album, for example) shows a "More in" poster rail of
its siblings, and every poster is the container's own artwork, so the row repeats one image. The
container's own page already lists those items as numbered rows. Music apps show that list on the
item page with the item marked; the poster rail suits containers whose items have their own stills.

## What Changes

- The Media detail page reads the nearest container's Entity with its items and applies the rule the
  Entity page uses to pick a numbered list (`containerTrackRows`: a `container` archetype whose
  member Media are all class `audio`). When it holds, the page renders that same list component
  (headings per stored sequence, number, title, differing people, duration, Play and overflow) in
  place of the "More in" rail, headed by the container's title linked to its page, with the
  current item's row marked `aria-current="page"` and highlighted.
- Any other container (video items, mixed or unknown classes) keeps the "More in" rail unchanged.
  The choice uses only archetype and Media class; no domain vocabulary.

## Non-goals

- No change to the Entity page, the Next link, the More with or Related rows, or native clients.
