# Mini player links to the artist and album

Ticket: CORE-3349 (`queue:core:main`).

## Why

The web's docked mini player shows the playing track's artwork, title and artists as plain text. A
listener who wants the artist or the album has to open the full player or search for them.

## What Changes

- Every credited person in the mini player's artist line links to their Entity page. No primary
  artist is chosen: a track with several artists links each one.
- The line also names the album (the first container credit) and links it to the album's page. The
  artwork links to the same page as a pointer shortcut, kept out of the tab order because the album
  name is the keyboard route.
- The links are in-app navigation. The player host sits above the routes, so playback continues.

## Non-goals

- Native clients; they get their own tickets if they want the same thing.
- The full player's audio stage, which already links its credits.
