# The web audio player offers every Oscillux Visual and lets the viewer choose

Ticket: CORE-2368 (`queue:core:main`)

## Why

The owner found that the web audio player shows few Visuals, has no way to change them, and that
they look blocky and alike. Core vendored 9 of the 61 web legs the pinned Oscillux v0.2.5 ships,
chose the Visual by hashing the album artwork, and fed the quality governor the clamped tick
interval, so a paused or throttled tab read as slow frames and dropped render scale to half.

## What changes

- `vendor/oscillux/` carries all 61 `visuals/*.web.frag` legs, byte-identical to the release, and
  `visuals.json` lists every Visual in Oscillux catalog order with its name and calm flag.
- Player settings for audio gain a Visualizer select beside the track-change motion selects:
  Rotate (default), One per album, or any single Visual by its Oscillux name. The choice is kept
  in this browser's localStorage.
- Rotate picks a different Visual for each track and changes Visual after about 40 s of drawn
  time on a downbeat (or 6 s later without one). Under reduced motion it picks only calm Visuals
  and does not change on a timer.
- The governor records the raw frame interval; the render step still clamps `dt`.

All of this is host selection policy and glue (ADR-0228). No effect code moves into core.

## Non-goals

- The Oscillux vivid palette tour and saturation defaults (CORE-2369), bloom and per-visual post
  (CORE-2370) and GPU timer-query timing (CORE-2371). They need an Oscillux export or are separate
  host work.
- No Profile-level or server-side setting.

## Failure modes

- A stored choice naming a Visual the release no longer has reads as Rotate.
- A blocked localStorage keeps the choice for the page only.
- A browser without WebGL2 or OfflineAudioContext shows no select, as it shows no Visual.
