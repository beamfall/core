# Web Now Playing for audio

Ticket: CORE-1637 (`queue:core:main`).

## Why

The web audio stage shows artwork, the title and credits, but the credits are plain text, nothing
says what plays next, and nothing says what the listener is hearing (codec, resolution, direct play
or transcode). The transport is the video transport: it offers picture-in-picture and fullscreen,
which do nothing useful for a track, has no shuffle or repeat, and fades away after three seconds
of playback, which hides the controls a listener reaches for most.

## What Changes

- The audio stage links the title to the item's detail page and each credit to its Entity page.
- The audio stage lists the next few items from the queue already driving playback (card queue,
  server Play Queue, or the server-issued next chain). Choosing one plays it. No second queue.
- A quality badge from playback-info `audioQuality` (CORE-1627): codec, bit depth and sample rate
  when known, and whether the file is direct played or transcoded.
- Shuffle and repeat (off, all, one) on the audio transport. They are a view over the existing
  queue: shuffle steps through a stable shuffled order of it, repeat all wraps at its ends, repeat
  one replays the item when it ends. The declared next item and gapless preparation follow them.
- On the audio stage the transport has no picture-in-picture or fullscreen controls, the
  fullscreen shortcut does nothing, and the controls never auto-hide. Chapter controls stay.
- Every new control has a visible or screen-reader label and is reachable by keyboard.

## Non-goals

- Server-side shuffle or repeat of the Play Queue. The modes are a client view and are not synced.
- Native clients.
- Listening controls such as speed, sleep timer and silence trimming (CORE-2960).

## Failure modes

- Without `audioQuality` (an older server) no badge is shown.
- With no queue and no issued next the Up Next list is not shown; shuffle is offered only with a
  queue of two or more items.
- Outside a router (tests, embeds) the title and credits render as plain text.

## Acceptance evidence

- vitest: `nowPlaying.test.ts` (quality label, shuffle and repeat neighbours, upcoming items).
- `npx tsc --noEmit` and `npm run lint` in `internal/web/app`.
- Desktop and phone-width screenshots of Now Playing.

## Rollback

Revert the change. No wire, schema or stored server state changes; the modes live in browser
storage only.
