# Abandoned rendition generations are failed, not left generating

Ticket: CORE-2054 (`queue:core:main`).

## Why

A precomputed playback rendition row stays `generating` forever in two cases:

- A run aborts while its encrypted Library is Sealed. Its failure write cannot reach the Library's
  private database, so the call returns without effect.
- The server stops mid-run. Nothing revisits the row on the next start.

`PlaybackRenditionCacheHealth` then counts those rows as generating indefinitely.

## What Changes

- The server tracks, in memory, the rendition generations it has started and not yet published or
  failed.
- At start, before job workers resume, every `generating` row that no run in this process owns is
  marked `failed`.
- When an encrypted Library is Unsealed, its `generating` rows that no live run owns are marked
  `failed` before the Library is registered for use.
- A generation still running in this process keeps its row and can still publish.

## Non-goals

- No schema or wire change. No failure reason column.
- The precompute run's own revocation check is unchanged.
