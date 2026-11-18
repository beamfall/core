# Related row: one card per title, no Extras, no filler

Ticket: CORE-2506 (`queue:core:main`).

## Why

Tower QA (2026-11-18, A Beautiful Mind) showed the Related row listing Disclosure Day three times
(two feature files in separate folders and a 3-minute trailer) and then an alphabetical run of the
library (Abigail, A Complete Unknown, A Walk to Remember, Air, Anora). `relatedMedia` dedupes only by
Media id, reads Extras through the `entity-media` Source, and keeps every Media that shares a single
category credit. A broad genre is shared by most of a library, so those one-signal ties fill the row
in the Source's title order.

## What changes

- `relatedMedia` leaves out Extras: Media with the Extra bit and Media that are the Extra side of an
  `extra-of` relation.
- `relatedMedia` lists one Media per title: Media with the same title (case and surrounding space
  ignored) and the same year collapse to the first-ranked one, and Media with the item's own title
  and year are left out.
- `relatedMedia` keeps only Media that share at least two of the ranking credits, at least one of
  them a category credit. A Media that shares only one genre is filler and is left out, so the row
  shows fewer cards, or none (the web already hides an empty row).

No wire change: `relatedMedia`'s arguments and result shape stay as published in client-core-wire
2.26.0.

## Non-goals

- Provider recommendations (TMDB "similar" lists). Core stores no such signal today.
- Grouping editions on the Related row beyond the title and year rule.
