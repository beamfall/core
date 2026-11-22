# Artist page: releases grouped by type, with years, and Entity radio

Ticket: CORE-2769 (`queue:core:main`).

## Why

The web artist page lists every album in one alphabetical "Albums" rail with no years, so a
discography reads nothing like Plex (Albums, EPs, Singles, Live, Compilations, newest first). The
music plugin provides the release type from track tags (plugin-music branch
`claude/core-artist-depth`). Core needs a way to split a related kind by those tags and show
years on related-Entity cards.

## What changes

- Store: related-Entity cards carry the earliest linked visible Media year. The same batched
  read derives a ruled card’s value from its currently linked visible Media.
- Hydration: track Media carry the release type. A one-time backfill copies a per-track tag
  when already available and reads the file tags otherwise. It writes that Media field alone.
- App hook `EntityGroupRules`: a plugin's glue names a kind, a declared text field and the ordered
  values with their headings. Rules for undeclared kinds or non-text fields are dropped with a
  warning. Core names no kind or value.
- Graph: `entity(id).relatedEntities` applies a rule to each owning Library's cards independently.
  Unruled cards retain their relation order and the owning plugin's archetype. Ruled cards are ordered by
  rule value (items without a value first, unlisted values last), newest first within each value,
  and the group gains `sections { value displayName count }` describing the consecutive runs.
  Clients that ignore `sections` see the same group with every item. Additive wire change.
- Stations: a generic `entity` seed kind seeds from any Entity id, so a client needs no plugin kind.
- Web: the Entity page heads each section of a split container kind with its heading and count, and
  an audio Entity page offers Radio, playing the `stationGenerate` window as the client queue.

## Non-goals

- Similar-entity rail for person Entities (artists share no Entity relation with categories; it
  needs a server query) — follow-up ticket.
- MusicBrainz lookup of a release type for untagged albums — follow-up ticket.
- The album track list and synced lyrics (other tickets).
