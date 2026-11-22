# Library navigation node IDs carry the prefix once

Ticket: CORE-2343 (`queue:core:main`).

## Why

The Navigation Manifest names a Library's node `"library-" + LibraryID`, but Library IDs are minted
with that prefix already, so every Library node read `library-library-films` and the web address
was `/n/library-library-films` (2026-11-17 tower QA, T19). The web collapsed the doubled word in
its addresses (CORE-2322) while the server kept sending it.

## What Changes

- A Library's navigation node ID carries the `library-` prefix once: it is the Library ID, or the
  ID with the prefix added when a Library ID lacks it.
- A stored navigation override saved with the old doubled IDs still hides, orders and relabels the
  same nodes. Where it names a node both ways, the current ID wins. A doubled ID that is itself a
  live node's ID (a Library titled "Library Films") names that node. The stored blob is not
  rewritten.
- The web resolves an old `/n/library-library-films` link to the node, and reads a Library pivot
  remembered under the old node ID.

## Impact

- Code: `internal/manifest`, web `src/lib/nodePath.ts` and `src/lib/libraryPivot.ts`.
- Wire: the manifest shape is unchanged; node ID values change. The manifest golden and the golden
  corpus are regenerated. Native clients that stored node IDs locally see the new values.
