# Console Overview, job queue and provider keys reflect the server's real state

Tickets: CORE-2310 (Overview), CORE-2309 (unavailable Library folder), CORE-2308 (job queue),
CORE-2311 (optional provider keys, UI part), CORE-2318 (activity indicator) (`queue:core:main`).

## Why

A QA pass on an owner's server found the Console Overview saying "Nothing needs attention" while
Library Health listed problems and scans had failed; the first-value step said there was nothing to
browse on a server with a full catalog (completed scans are pruned after a day, so only a later
failed scan was on record); "Fix unmatched" counted one page of the queue; the job queue buried live
work under hundreds of finished polls; optional provider keys (TheTVDB, AniList, Google Books) were
shown as missing, and plugins no Library uses asked for keys; the header activity link polled the
jobs read without an Admin Session and logged refusals.

## What Changes

- Overview "Needs attention" lists unresolved failed jobs (redacted error, link to the Library's
  folders when the job targets one), Library health counts summed over all Libraries, an unsaved
  backup key, and unavailable Library folders linking to that Library's folders.
- The first-value step shows a running scan as Scanning, never says there is nothing to browse while
  items are browsable, and counts browsable content as landed when no scan on record stopped part-way
  through an import.
- "Fix unmatched" shows the Library health unmatched total.
- The job queue lists live work, then unresolved failures, then one folded history row per job kind,
  with relative times, durations and error text.
- A scraper credential the server reports as not required shows "Optional"; a missing key is asked
  for only when a Library uses the plugin. Server side, `credentialRequired` is false for a scraper
  whose capability the plugin's embedded default key already covers (owner decision: TheTVDB is
  optional; TV matches through the embedded TMDB key).
- The header activity link reads jobs only while an Admin Session is known active.

## Impact

- Web: `internal/web/app/src/areas/console/**`, `src/lib/providerKeys.ts`,
  `src/components/beamfall/ServerActivity.tsx`, string catalogs.
- Go: `internal/graph/graph_consoleconfig.go` (`credentialRequired` semantics).
- No wire shape change: the Console selects more existing `jobs` fields itself.
