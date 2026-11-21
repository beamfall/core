# Home hero names what it features; audio cards name the artist first

Ticket: CORE-2871 (`queue:core:main`).

## Why

The owner found the web Home hero filling most of the screen. For an episode it showed only the
episode's own title and summary, with no show name and no details. Track cards led with the album,
so a long album title truncated the artist away.

## What Changes

- **Web Viewer hero**: the hero is shorter (340px at desktop width, 300px on mobile). It is headed
  the way cards are headed, so an episode shows its show and then the episode line. A facts row
  shows the byline, year, rating, runtime and audience score. The summary is dropped when it repeats
  the lead or one of the facts, and it is hidden on mobile.
- **Audio card subline**: the artist comes before the album.

## Impact

`Hero.tsx`, `chrome.css`, `cardTitles.ts`, their unit tests, and the viewer Home goldens.
