# Music wording and navigation in the web viewer

Tickets CORE-2764, CORE-2765 and CORE-2768 (`queue:core:main`).

## Why

Audio pages use watching language, imply a play ranking without play history, and make
listeners open a track's details to find its artist or album.

## What changes

- Use listening labels for audio listings and item actions.
- Rank an artist's tracks using the acting profile's plays and use “Tracks” when no ranking
  is available. Keep the full list in server order.
- Link audio track menus to credited people and containers. Show credited people and release
  year on audio container cards when available.

Use existing catalog and engagement reads. Missing metadata leaves its menu link or subtitle
component absent. Wire changes and server-side aggregate play counts are outside this change.
Verify with focused viewer tests, typecheck, lint and catalog parity. Reverting the viewer
change restores the previous presentation.
