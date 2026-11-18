# Web visualizer GL resilience

Tickets: CORE-2481 (feedback format fallback), CORE-2482 (WebGL context loss and restore),
CORE-2516 (resize and device-pixel size), CORE-2517 (reduced motion for pinned and per-key
visuals), CORE-2483 (a 30 Hz browser cap read as overload).

## Why

The web visualizer checks only `EXT_color_buffer_float`. Where it is missing the feedback drops
to RGBA8, which clips HDR feedback and steps gradients, and where it is present but the float
framebuffer is incomplete the renderer throws. Separately, a `webglcontextlost` event turns the
visual off until the component remounts, although browsers restore contexts routinely.

## What Changes

- The renderer tries RGBA16F with `EXT_color_buffer_float`, then RGBA16F with
  `EXT_color_buffer_half_float`, then RGBA8. Each candidate is checked with
  `checkFramebufferStatus` on a 1x1 probe attachment, and the probe resources are deleted. The
  chosen format is exposed as `Renderer.feedbackLabel` (`float`, `half-float`, `8-bit`).
- On `webglcontextlost` the visualizer calls `preventDefault()`, stops its animation-frame loop
  and drops the dead renderer. On `webglcontextrestored` it builds a new renderer (programs,
  textures, framebuffers), re-applies the selected visual and artwork, and resumes with fresh
  feedback and limiter state. Its listeners are removed on unmount.

- Resizing keeps the flash limiter's budgets and history: only the size-dependent `rows` and
  output textures are rebuilt, and the last output is scaled into the new one, so a resize cannot
  reset the token budget and admit a burst of flashes (CORE-2516).
- The canvas backing size comes from a `ResizeObserver` with `device-pixel-content-box` (exact
  device pixels, DPR capped at 2), falling back to the CSS box, instead of a per-frame
  `getBoundingClientRect()` (CORE-2516).
- With reduced motion, a pinned visual that is not calm, and the per-key visual, are drawn from
  the calm visuals, as rotation already was (CORE-2517).
- A cadence gate sits between frame intervals and the Oscillux governor: once a steady cadence at
  a 30 Hz browser cap is seen, intervals are scaled to the 60 Hz target, so power-saving caps no
  longer halve the render resolution; slower or unsteady frames still reach the governor raw
  (CORE-2483). WebGL timer queries are not exposed by default in current browsers.
- The limiter's `rows` pass binds the previous output as `uHist` (Oscillux v0.2.9, CORE-2515).

## Non-goals

- Changing the Oscillux shaders, the limiter, or the quality governor.
- Native clients.

## Failure modes

- A restore that cannot create a renderer, or fails to apply the visual, leaves the slot empty
  as for any other visualizer failure; playback is never interrupted.

## Acceptance evidence

- vitest: `renderer.test.ts` (fallback order, probe cleanup, limiter state kept over a resize),
  `Visualizer.context.test.tsx` (loss, restore, failed restore, listener removal),
  `Visualizer.test.tsx` (`backingSize`, `chosenVisual`), `cadence.test.ts` (synthetic 30 Hz
  clock through the vendored Oscillux governor).

## Rollback

Revert the change. No schema, wire or stored state is touched.
