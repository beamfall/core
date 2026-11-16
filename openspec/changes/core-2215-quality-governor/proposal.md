# The Web Viewer visualizer scales its render size and detail with the Oscillux quality governor

Ticket: CORE-2215 (`queue:core:main`).

## Why

The Web Viewer visualizer always renders its Oscillux visual at the full canvas size, up to a
device pixel ratio of 2, with no detail reduction. On a large or high-density display a GPU that
cannot hold that size drops frames, and playback looks choppy. Oscillux's reference web host
avoids this by driving the render size and the visual's detail reduction from the Oscillux
quality governor; core's glue does not.

## What Changes

- The renderer creates an Oscillux `Governor` from the wasm core with the reference defaults
  (`GovernorConfig`, four levels, starting at full quality) and feeds it each frame's interval
  while no switch is running, as the reference host does without a GPU timer query.
- Each level maps to the reference host's ladder: render scale 0.5, 0.67, 0.83 or 1 and detail
  reduction 1, 0.6, 0.25 or 0. The visual and its feedback, and a running switch, render at the
  scaled size; the presentation uniform carries the detail reduction. The present pass, the frame
  history and the flash limiter stay at the full canvas size, so the limiter keeps its state when
  the level changes.
- A level change is applied only between switches, ends nothing else, and keeps the visual's
  trails by scaling its feedback into the new size.
- Host glue only: core writes no governor logic of its own and edits no vendored byte.

## Impact

- Web app only (`visualizer/renderer.ts`); no wire, store or contract change.
- The GPU timer-query path of the reference host is not wired (most desktop browsers on macOS do
  not expose `EXT_disjoint_timer_query_webgl2`); the frame interval drives the governor.
