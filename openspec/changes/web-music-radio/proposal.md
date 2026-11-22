# Radio in the web viewer

Ticket: CORE-2772 (`queue:core:main`), server generator and web part.

## Why

The server can already generate and start a Station (ADR-0098 area 5) from a track, an Entity or a
Collection, but the generator only replays the seed's own Media in a shuffled order, its rules
cover only year and duration, and the web viewer offers no way to start one. Listeners expect
Plex-style radio: start from an artist, album, track or genre and keep hearing related music.

## What Changes

- The Station generator expands from the seed to related Media using data the catalog already
  holds: Entities shared with the seed (credits, containers, categories), release era and the
  Profile's play history. No audio analysis is involved; sonic similarity stays with the separate
  sonic-mixes work in Oscillux.
- Generic seed kinds `media` and `entity` are accepted beside the existing kinds, so a client never
  names plugin vocabulary.
- Station rules accept `decades` (a list of decade start years) and `anyEntityIDs` (a candidate must
  link to at least one, used for genre and mood). Rules stay opaque JSON; unknown fields are still
  ignored.
- Each generated item carries a localized explanation naming its strongest reason; the Station-level
  scarcity note is localized too.
- Web viewer: a Start radio action on audio Entity pages (artist, album, genre) and in the track
  menu. Starting replaces the server play queue after a confirmation when it already has items,
  then plays it in server-queue mode. The player shows why the current track was picked, offers
  decade and category tuning, and tops the queue up with a fresh generation through `queueAdd`
  when playback nears its end.

## Non-goals

- Native clients (Apple, Android). They consume the same API later.
- Sonic or audio-feature similarity (sonic-mixes, Oscillux).
- Saved-station management UI. `stationSave` and `stations` stay available but unmounted.
- Changes to the play-queue store or resolvers; the web only consumes `stationStart`, `queueAdd`
  and the `queue` source.

## Wire and schema

No GraphQL field, argument or response shape changes. The new rule fields live inside the existing
opaque `rulesJSON` string, and the new seed kinds are values of the existing `seedKind` string.

## Failure modes

- A seed outside the caller's visible, unsealed music Libraries is an unknown seed, as today.
- A rule set that leaves no candidates returns an empty window with the localized empty note; the
  web shows it instead of replacing the queue.
- A failed top-up leaves the current queue playing and retries on the next track change.

## Acceptance evidence

- Go (`internal/graph`): similarity expansion reaches related Media through a shared Entity, decade
  and entity rules filter before ranking, the seed track leads a track station, explanations are
  localized, generic seed kinds work.
- vitest: radio library (start, confirm, top-up), Start radio entry points, player reason line.

## Rollback

Revert the change. No schema, migration or stored state is added.
