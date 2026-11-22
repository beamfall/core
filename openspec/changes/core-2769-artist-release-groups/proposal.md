# Artist page: releases grouped by type, with years, and Entity radio

Ticket: CORE-2769 (`queue:core:main`).

## Why

The web artist page lists every album in one alphabetical "Albums" rail with no years, so a
discography reads nothing like Plex (Albums, EPs, Singles, Live, Compilations, newest first). The
music plugin now records a release type on each album (plugin-music branch
`claude/core-artist-depth`, field `release-type`), but core has no generic way to split a related
kind by a field, and related-Entity cards carry no year.

## What changes

- Store: related-Entity cards carry their year (the earliest linked visible Media), as Source cards do.
- App hook `EntityGroupRules`: a plugin's glue names a kind, a declared text field and the ordered
  values with their headings. Rules for undeclared kinds or non-text fields are dropped with a
  warning. Core names no kind or value.
- Graph: `entity(id).relatedEntities` keeps one group per kind. A ruled kind's items are ordered by
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
