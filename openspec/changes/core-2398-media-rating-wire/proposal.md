# Cards and the media read carry a content rating and an audience score

Ticket: CORE-2398 (`queue:core:main`)

## Why

Plex and Infuse show a content rating badge (PG-13, TV-MA) and an audience score on a title's
hero and detail screens. Core stores a plugin's certification as the conventional `certification`
Media field for the Profile ceiling, and CORE-2399 has the Film & TV plugin write
`certification-display` and a 0-10 `community-rating`, but no wire read carries either. A client
would have to fetch every item's `mediaFields` to show them, and the demo seed has none, so no
client can show or verify ratings in demo mode.

## What changes

- `SourceItem` and `MediaInfo` gain optional `certification` (a display string) and
  `audienceScore` (a number from 0 to 10).
- `certification` is the item's `certification-display` text field, or its `certification` text
  field when it has no display value. `audienceScore` is its `community-rating` number field.
  These names are core convention, as `certification` already is; the values are plugin data.
- Cards read both in the per-card media read that sets `mediaClass` and `year`. `media(id)` reads
  them from the item's fields.
- The demo seed gives its films a `certification` and its series a `certification-display`, each
  with a `community-rating`.
- The next client-core-wire minor carries the added fields.

## Non-goals

- No plugin ingest. TMDB and OMDb scores and certifications are CORE-2399, CORE-0363 and
  CORE-0370.
- No score source label on the wire. One conventional score field has one source per item; a label
  can follow when a plugin writes more than one score.
- No change to the Profile certification ceiling, which still ranks `certification` only.

## Failure modes

- An item without the fields omits both keys.
- A `community-rating` outside 0-10 is not a score on this scale, and the key is omitted.
- An empty `certification-display` falls back to `certification`.
