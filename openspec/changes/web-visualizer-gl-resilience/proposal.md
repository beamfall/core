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

- A resize never hands the flash limiter a fresh budget (CORE-2516). At the same display scale
  the size-dependent textures are rebuilt, the texels that were in view are copied one to one from
  the top left, everything else starts black and tracked black, and the grant state is written
  with every budget spent, as it is at the first size. A paused player draws a frame through a
  grant state the host wrote before it holds the picture, and a spend during the fade of a
  display-scale change starts the fade again.
- Pixels come into view only as whole texels that start black. A slot grown past a texel it cut
  holds the view, fades to black and starts again, and the canvas is clipped to the pixels the
  limiter has judged in view (CORE-2516).
- The canvas backing size comes from a `ResizeObserver` with `device-pixel-content-box` (exact
  device pixels, DPR capped at 2), falling back to the CSS box, instead of a per-frame
  `getBoundingClientRect()` (CORE-2516).
- With reduced motion, a pinned visual that is not calm, and the per-key visual, are drawn from
  the calm visuals, as rotation already was (CORE-2517).
- A cadence gate sits between frame intervals and the Oscillux governor: once a steady cadence at
  a 30 Hz browser cap is seen, intervals are scaled to the 60 Hz target, so power-saving caps no
  longer halve the render resolution; slower or unsteady frames still reach the governor raw
  (CORE-2483). WebGL timer queries are not exposed by default in current browsers.
- The limiter's `rows` pass binds the previous integer tracker state as `uTrack` (CORE-2515).

## Non-goals

- Changing the Oscillux shaders, the limiter, or the quality governor.
- Native clients.

## Failure modes

- A restore that cannot create a renderer, or fails to apply the visual, leaves the slot empty
  as for any other visualizer failure; playback is never interrupted.

## Acceptance evidence

- vitest: `renderer.test.ts` (fallback order, probe cleanup, budgets spent and the view held over a resize),
  `Visualizer.context.test.tsx` (loss, restore, failed restore, listener removal),
  `Visualizer.test.tsx` (`backingSize`, `chosenVisual`, `viewClip`), `cadence.test.ts` (synthetic 30 Hz
  clock through the vendored Oscillux governor).

## Rollback

Revert the change. No schema, wire or stored state is touched.
