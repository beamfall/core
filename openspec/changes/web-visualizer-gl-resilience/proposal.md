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
  flash limiter (grant state written spent, area limit zero, limiter fed black, until a frame of
  the fade held no fall back, which the grant word reports, and for 2 s of credited time at
  most), black is presented for 0.1 s of credited time, and a frame starts at the geometry asked
  for then, black and tracked black with every budget spent, as the first frame does. A change during
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
- A geometry change costs the picture: the frame is held until the limiter grants its fall, the
  visual is black until the start, and the new frame rises through the grant as a first frame
  does. A drag that outlasts the black starts and withdraws a frame until the slot is still.
  This is the price of one rule with no pixel carried over.
- The fade and the black are counted in the time the limiter is credited with, not on the clock.
  The page cannot read the display's refresh period or when a frame was painted, and its
  animation-frame cadence does not give it: Safari runs animation frames at 60 a second on a
  120 Hz display, where a frame painted a refresh late and followed on time is shown half the
  interval. So a steady cadence credits half of each interval (`shownMs`), bounded by the
  shortest of the last 32 draw intervals, and credited time takes about twice as long on the
  clock at 60 frames a second. Measuring the cadence cannot safely shorten this, since the
  credit must stay a lower bound: crediting more needs the limiter to take a correction for a
  frame after its successor is drawn, or the platform to report presentation times. Modelled there at the default 3 Hz: a dark frame is black at once and starts
  about 0.25 s after the change; a frame with luma to drop is held about 0.7 s (two luma tokens,
  0.33 s credited), is black about 0.25 s and rises about 0.7 s after the start; a full-frame
  saturated red is held about 2.7 s (the red token, 1.33 s credited) and rises about 2.7 s after
  the start. The 2 s cap on the fade is about 4 s and is reached only if the grant word never
  clears.
- After a change of device pixel ratio the cached CSS box says nothing about the slot's physical
  pixels. Until the slot is observed at the new ratio the frame on screen is held undrawn: a
  zoom freezes the visual for a frame or two, three at most, and a slot with no size stays
  frozen until it has one.
- The grant word is read through a pixel-pack buffer and a fence, never by a blocking
  `readPixels`: the fade ends a frame or two after the grant, later on a busy GPU, and at the
  2 s cap if no read ever finishes.
- A slot that shrinks and grows back between two draws costs a fade: the half it hid cannot come
  back into a frame whose view only shrinks.

## Acceptance evidence

- vitest: `renderer.test.ts` (fallback order, probe cleanup, the one path for a geometry change),
  `Visualizer.context.test.tsx` (loss, restore, failed restore, listener removal),
  `Visualizer.test.tsx` (`backingSize`, `chosenVisual`, `viewClip`, the black slot), `cadence.test.ts` (synthetic 30 Hz
  clock through the vendored Oscillux governor).

## Rollback

Revert the change. No schema, wire or stored state is touched.
