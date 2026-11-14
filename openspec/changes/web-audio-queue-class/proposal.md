# Keep audio play queues audio-only and route audio to the audio stage from the start

Tickets: CORE-1490 and CORE-1309 (`queue:core:main`).

## Why

CORE-1490: playing a track from a mixed shelf (Home > Recently Added) builds a play queue from every
playable row, so videos sit between tracks ("3 of 4") and next/previous or auto-advance can jump
from a track into a video.

CORE-1309: a music track opened the video player. The audio stage, artwork, credits, queue
controls and truthful delivery label landed with `web-audio-player` (CORE-1143). One path still
shows the bare video frame: while playback-info loads, the page does not know the class yet and
renders the black `<video>` with a spinner. The ticket's visualizer criterion needs the pinned
Oscillux release, which core cannot consume yet.

## What Changes

- A play-queue entry may carry the Media's class when the row that built it reports one (search
  results do). The player also learns classes from playback-info: the current item's
  `mediaClass` and the declared next item's `next.mediaClass`.
- The player narrows its queue to the playing item's class: entries whose known class differs are
  dropped, entries with no known class stay, the playing item always stays. Position ("2 of 3"),
  previous/next, auto-advance and the queue carried to the next item all use the narrowed queue,
  so a dropped entry stays dropped.
- When the playing item's class is known before playback-info answers (from its queue entry), the
  player shows the audio stage instead of the empty video frame while loading.
- The audio stage reserves an empty, labelled slot where a future Oscillux visualizer mounts. No
  visual effect code is added.

## Non-goals

- A visualizer or any effect code. Visual effects come only from Oscillux (ADR-0228); mounting the
  pinned release is a follow-up once core can consume it. CORE-1309's visualizer criterion stays
  open.
- Adding `mediaClass` to graph `SourceItem` rows. That is a wire contract change (new contract
  version and consumer lockstep). Without it, Home shelf rows carry no class, so the player only
  discovers a video when it is the declared next item; a fully audio-only queue from a mixed shelf
  needs the wire field (follow-up).

## Failure modes

- Unknown class: the entry stays in the queue, which is the previous behaviour.
- A nav-state queue from an older build without classes reads as unknown classes.

## Acceptance evidence

- vitest: the queue narrowing rule (`watchQueue.test.ts`), audio-vs-video routing including the
  loading phase, and a learned video next item being skipped (`PlayerPage.test.tsx`), the empty
  visualizer slot (`AudioStage.test.tsx`).

## Rollback

Revert the change; no server, schema or stored state is touched.
