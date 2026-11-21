## Why

CORE-2845 lets listeners add tracks to the server Play Queue from catalog and playlist menus and continue their current listening run from that queue.

## What Changes

- Offer Play next and Add to queue for playable audio Media with known lock state.
- Import the complete catalog run on its first explicit queue edit, using the active Media as the cursor.
- Serialize edits with playback advancement and confirm the result with a scoped server read.

## Impact

The web viewer uses the existing Play Queue reads and mutations.
