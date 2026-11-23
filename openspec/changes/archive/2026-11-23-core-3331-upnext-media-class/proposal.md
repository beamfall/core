# Split Up Next by media class

Ticket: CORE-3331 (Home Up Next mixes TV episodes with music tracks).

## Why

Home's Up Next shelf walks every ordered container the Profile engaged with:
seasons and albums alike. A viewer who watched a TV episode and played two
albums sees the next episode between the next tracks, and cannot choose which
kinds of media the shelf offers.

## What changes

- The `next-up` Source takes an optional `value` naming a media class
  (`video` or `audio`). With a class, only containers whose engaged and
  frontier children are of that class contribute. With no value it keeps
  offering every class, so plugin sections that declare `next-up` without a
  value behave as before. An unknown value yields nothing.
- Home's Up Next shelf (`next-up`) reads `next-up` with value `video`.
- Home gains a Continue Listening shelf (`continue-listening`) after Up Next,
  reading `next-up` with value `audio`.
- The viewer chooses which shelves Home shows, and their order, with the
  existing per-Profile Home layout editor (CORE-1972): both are ordinary
  section ids. A saved order or hidden set for `next-up` keeps applying.

## Out of scope

- Apple clients do not send a Source's `value` yet (CORE-3332); until that
  lands they show both shelves unfiltered.

## Impact

- Wire: no shape change. `SourceSpec.value` already exists; source value
  vocabularies are not part of the wire contract, so no consumer repin.
- Storage: none.
