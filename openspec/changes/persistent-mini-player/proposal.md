# Persistent mini player

Ticket: CORE-1633 (`queue:core:main`).

## Why

The web player's media element lived inside the `/watch/:id` route, so leaving the route for any
other viewer page stopped the music. Every other music client keeps a track playing while the
listener browses, with a small docked player to control it.

## What Changes

- A player host above the viewer routes owns the player and its media element. The watch route
  attaches to the host, which shows the full player as before.
- When the watch route closes while an audio Media plays, playback continues and the host shows a
  docked mini player on every viewer page: artwork, title, credited people, play/pause, next, a
  progress bar, an Open player control that returns to the full player on the same element, and a
  Stop control.
- A video Media keeps the full-screen watch route: leaving the route stops it, as before.
- While docked, an advance (end of track, Next, remote skip) plays the next item in place without
  leaving the page being browsed.
- The relock pause (ADR-0004), progress beacons, Media Session, remote control target and Watch Party
  keep driving the same element. A relock, a terminal failure or an advance into a non-audio Media
  closes the mini player. A Profile switch ends playback.
- New strings in a `mini-player` shard in every shipped locale.

## Non-goals

- Gapless transitions (CORE-1634, a separate change).
- Persisting playback across a page reload.
- A mini player for video (picture-in-picture is the browser's own control).

## Capabilities

### New Capabilities

- `persistent-mini-player`: audio playback that outlives the watch route, and the docked player.

### Modified Capabilities

None.
