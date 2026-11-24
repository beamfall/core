# Karaoke party: clearer up-next, per-screen join QR and mute, undo a skip

Tickets: CORE-3378, CORE-3379 and CORE-3380 (`queue:core:main`). Owner feedback 2026-11-23.

## Why

On the stage ticker the singer and the song ran together, with no sense of when each turn comes.
Not every stage screen needs sound or a join code in the same place. A host who skips a singer
who did not answer had no way to bring them back when they turned up.

## What changes

- The stage ticker and the idle up-next list name each entry's singer, song and artist as
  separate parts, with the server's ETA ("next", or "in about N min") (CORE-3378). Web only.
- Each stage screen carries a join-QR corner (off, or one of four corners) and a mute flag, stored
  on the screen and set by the host from the Screens panel (CORE-3379). A muted stage plays
  silently and never asks for a sound gesture.
- Skipping the current singer records the entry as skipped rather than done. The host gets an
  Undo right after the skip and can bring back any skipped entry later. It returns as next up and
  the current singer keeps singing (CORE-3380).
- CORE-3379 and CORE-3380 change the wire contract: the landing stack publishes the next
  `client-core-wire` minor.
