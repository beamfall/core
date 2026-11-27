# Re-hydrate a moved file when the move changes a path-derived fact

Ticket: CORE-1733 (`queue:core:main`).

## Why

CORE-0368 keys hydration on the content fingerprint alone. A mapper also derives facts from the
file's path (a disc number from a `CD2` folder, for example), and a moved file keeps its bytes and
its Media, so the seam skipped it and the path-derived facts kept their old values.

## What Changes

- The hydration key the seam compares and records is the content fingerprint plus a digest of the
  plan the mapper produced from the tags and the path. A move that changes a derived fact gives a
  new key and a replace write; an unchanged file, or a move that changes no derived fact, still
  writes nothing.
- An empty content fingerprint still yields an empty key, so the mapper's no-fingerprint rule is
  unchanged.
- Keys recorded before this change hold the bare fingerprint, so each such Media gets one replace
  write on its next scan. Replace keeps operator, manual and locked values.

## Non-goals

- No mapper or plugin API change and no wire change.
