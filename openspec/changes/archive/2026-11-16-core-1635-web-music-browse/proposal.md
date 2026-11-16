# Web music browse: group a container's tracks by stored sequence

Ticket: CORE-1635 (`queue:core:main`).

## Why

The listening browse change (`core-1635-listening-browse`) numbers an audio container's rows in
server order and left disc grouping as a gap, on the premise that the disc is a plugin field the
viewer cannot read. It is not: core's scan stores every parsed Media's generic ordinals as the
Media fields `sequence` and `index` (from the SDK's `ParsedMedia.Sequence` and `Index`), and the
existing `mediaFields(mediaID)` query already returns them. A multi-disc container can therefore
be grouped by disc, and each row can show its stored number, without a wire or schema change.

## What Changes

- The web viewer's row read for an audio container also reads `mediaFields(mediaID: $id)` in the
  same document and keeps only the positive numeric `sequence` and `index` values. Person pages
  and playlists keep the plain row read.
- Layout rule (documented in `internal/web/app/src/areas/viewer/lib/trackList.ts`): when every
  listed Media has a stored `sequence` and they hold at least two values, the track list groups by
  `sequence`, ascending, under a "Disc {number}" heading per group. Within each group, or the whole
  list when it does not group, rows are numbered and ordered by stored `index` when every row has
  one and none repeats; otherwise they keep server order and are numbered from 1.
- Play from any row, and the page's primary Play action, start and queue the rows in display
  order, across groups: the Entity page reads the rows once and both entry points share that order.
- One new string key (`trackGroupLabel`) in the `listening` shard, in every shipped locale.

No GraphQL schema, wire contract, conformance golden or plugin change.

## Gaps (not in this change)

- A container whose tracks carry no stored `sequence` (for example untagged rips split only by
  folder) is not grouped; its rows keep server order and position numbers.
- The heading reads "Disc" for any audio container that groups; there is no plugin-supplied
  grouping label.
