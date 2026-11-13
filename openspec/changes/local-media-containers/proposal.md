# Group local files into plugin containers without a provider

Ticket: CORE-1411 (`queue:core:main`), split from CORE-1148.

## Why

A Library of local files with no provider credentials never gets the plugin's container Entities.
For the Film & TV plugin that means episode files are never grouped into a show and its seasons:
Core writes container Entities only on the provider-apply path, and the per-file title is the
parsed container title (the series), so every episode in a season lists under the same name.

plugin-filmtv v0.1.7 already exposes the local grouping it parses from a path
(`filmtv.LocalHydration`), declares the container relations and a hydration completion flag, and
offers the parsed per-file title (`(*filmtv.Parser).MediaTitle`).

## What Changes

- `internal/scan` gains an optional parser extension, `MediaTitler`. When the Library's parser
  implements it and returns a non-empty title for a file, that title replaces the parsed title as
  the Media title on import, in both scan paths. Identification still receives the parsed title,
  and a provider title still replaces it (the store keeps an identified title across rescans).
- `cmd/beamfall` wires a Film & TV hydration mapper. For each file it reads
  `filmtv.LocalHydration(path)` and, for an episode, writes a season Entity (scoped by its series
  title, with its season number) linked to the Media through `season-episode` in episode order, and
  links the season to the show Entity through `season-of`. It marks every visited file complete
  with `filmtv-hydrated`, so a file is hydrated once.
- Existing Libraries are backfilled by the existing hydration backfill: Media without the
  completion flag are hydrated on the next scan.

## Non-goals

- Enriching local containers from a provider. Providers identify Media directly and never create
  show or season Entities, so the local containers are never duplicated; a provider-written
  container (a later change) will need to adopt them.
- Fix-Match prefill: the Fix-Match dialog prefills from the Media title, which for an episode is now
  the episode title. Filed as a follow-up.

## Failure modes

- A path the parser cannot read as an episode writes no Entities; the file is still marked complete
  and lists ungrouped, as today.
- The first scan after upgrade probes each video file that lacks the completion flag once (the
  backfill path re-reads tags). Later scans skip them.

## Acceptance evidence

- `internal/scan`: `TestMediaTitlerReplacesParsedTitle`, `TestMediaTitlerEmptyKeepsParsedTitle`.
- `cmd/beamfall`: `TestFilmTVHydrationGroupsEpisodeIntoSeasonAndShow`,
  `TestFilmTVHydrationMarksMovieComplete`, `TestFilmTVHooksRegisterHydration`.
- The plan validates against the pinned plugin-filmtv v0.1.7 bundle (`validatePlan` in the test).

## Rollback

Revert the change. Show and season Entities already written stay as ordinary Entities and can be
removed from the Library's Entity view; episode titles return to the series title on the next scan.

## Capabilities

### New Capabilities

- `local-media-containers`: container Entities and per-file titles from local files without a provider.

### Modified Capabilities

None.
