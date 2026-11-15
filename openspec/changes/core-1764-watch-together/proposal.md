# Watch together from the media detail page

Ticket: CORE-1764 (`queue:core:main`). Follow-up to CORE-1677.

## Why

A Watch Party can start only from the player bar (CORE-1677), so a household has to begin
playback before it can gather. The media detail page is where people decide what to watch
together.

## What Changes

- The web media detail page offers a ghost "Watch together" secondary next to Play/Resume. It is
  shown only once the server accepts the party clock calibration, which it allows on the home
  network only (CORE-0531), and only while no party is running in the tab. The probe runs once per tab.
- Choosing it starts the party through the existing create path (`startWatchParty`, CORE-1677),
  held at the viewer's start position (the saved position, or the beginning). It then opens the
  player past pre-play, with a navigation-state flag that opens the Watch Party invite panel. The
  player adopts the running party timeline and holds at that position until a member presses play.
- When the start fails, the page stays and shows a short notice: home network only, or failed.
- The party store, its panel and the wire contract are unchanged; the player reads one additive
  navigation-state flag.

## Impact

- Web viewer: `areas/viewer/lib/watchTogether.ts`, `areas/viewer/components/WatchTogetherButton.tsx`,
  `MediaDetailPage.tsx`, one line in `PlayerPage.tsx`, strings in `strings/media-detail.ts`.
- No server, GraphQL or wire-contract change.
