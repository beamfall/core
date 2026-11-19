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

- Every change to what the viewer sees takes one path (CORE-2516), whether the backing size, the
  display scale or the texels in view changed: the frame on screen fades to black through the
  flash limiter (grant state written spent, area limit zero, limiter fed black, for 2 s of
  credited time), black is presented for 0.1 s, and a frame starts at the geometry asked for
  then, black and tracked black with every budget spent, as the first frame does. A change during
  the fade or the black only moves that target, so a slot dragged to a new size costs one fade
  and one start. No texel is carried from one geometry to the next: the copy paths are gone.
- A frame presents only the whole texels its slot shows. Until the next frame starts the canvas
  keeps its size and position, clipped to the smallest slot seen, so a shrinking slot takes pixels
  out of view by the page's clip and never drops them to the backdrop. A paused player draws a
  frame through a grant state the host wrote before it holds the picture. The slot behind the
  canvas is black, not the theme's scrim, so a pixel with no judged frame shows the level its
  tracker starts at (CORE-2516).
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
- A geometry change costs the picture: the visual is black from the end of the fall the limiter
  grants until about 2.1 s after the change, and a drag longer than that repeats the cycle. This
  is the price of one rule with no pixel carried over.

## Acceptance evidence

- vitest: `renderer.test.ts` (fallback order, probe cleanup, the one path for a geometry change),
  `Visualizer.context.test.tsx` (loss, restore, failed restore, listener removal),
  `Visualizer.test.tsx` (`backingSize`, `chosenVisual`, `viewClip`, the black slot), `cadence.test.ts` (synthetic 30 Hz
  clock through the vendored Oscillux governor).

## Rollback

Revert the change. No schema, wire or stored state is touched.
