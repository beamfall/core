# The Web Viewer visualizer blends between visuals with the Oscillux switch pass

Ticket: CORE-2219 (`queue:core:main`).

## Why

When playback moves to an item with different artwork, the Web Viewer audio visualizer changes
its Oscillux visual in one frame: the incoming visual takes over the outgoing one's feedback, but
nothing blends the two. Oscillux ships a host switch pass for exactly this, and under ADR-0228
the transition must come from Oscillux, not from core.

## What Changes

- Vendor `switch/switch.frag` from the pinned Oscillux v0.2.1 GLSL ES 3.00 leg, byte-identical
  (SHA-256 as in the bundle's `index.json`), and record it in the vendored README and `pin.json`.
- During a switch the renderer draws both visuals, each with its own feedback (the incoming one
  seeded from the outgoing frame) and image input, and runs the switch pass between them before
  the present pass and the frame flash limiter. Style Luma Key, full motion, over 1.2 s with
  smoothstep easing, timed by the wasm core's transition scheduler; the outgoing visual is dropped
  when the scheduler reports the switch done.
- A change requested during a switch waits for it to end. A size change ends a switch.
- Items that share artwork keep one visual, as before; under reduced motion the visual still
  changes at once.
- Host glue only: core writes no transition shader and edits no vendored byte.

## Impact

- Web app only (`visualizer/renderer.ts`, `vendor/oscillux/`); no wire, store or contract change.
