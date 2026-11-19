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
- A frame presents only the whole texels its slot shows when it starts. Only the draw loop
  changes which canvas pixels are in view: it sizes and clips the canvas from the frame on
  screen (its size and its texels in view at its start), and neither the slot nor the audio
  stage clips the canvas, so a slot that shrinks crops nothing and one that grows back shows
  nothing again, observed or not. With no frame the canvas has no size. A paused player draws a
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
  does. A drag that outlasts the black stays black: a frame, the first after a mount included,
  starts once two draws running ask for one geometry, and a started frame withdrawn before its
  paint costs another 0.1 s of black, so a moving slot allocates the full-resolution targets
  once in that time at most and not at every frame. Framebuffer completeness is checked at
  every start: a target can be incomplete at one size and not another, and a limiter that reads
  an incomplete target's zeros passes anything. An incomplete target frees the frame, presents
  black and turns the visualizer off, as any other GL failure does.
  This is the price of one rule with no pixel carried over.
- The fade and the black are counted in the time the limiter is credited with, not on the clock.
  The page cannot read the display's refresh period or when a frame was painted, and its
  animation-frame cadence does not give it: Safari runs animation frames at 60 a second on a
  120 Hz display, where a frame painted a refresh late and followed on time is shown half the
  interval. So a steady cadence credits half of each interval (`shownMs`), and no interval more
  than B, the shortest draw interval since the visualizer mounted (4 ms at least, 1/24 s at
  most): `min(B, max(interval / 2, interval - B))`. Credited time takes about twice as long on
  the clock at 60 frames a second. B is never forgotten: one taken over recent draws rises when
  the page slows on the same display (120 to 30 draws a second at 120 Hz) and then credits two
  refreshes to a frame shown one.
  The credit is not a lower bound on the time shown in every case. It is one where B is no
  longer than the display's refresh period, which takes two draws one refresh apart since the
  mount. It over-credits where the page has only drawn slower than its display: steady 30 draws
  a second since the mount on a 120 Hz display credits 16.7 ms, and a frame painted three
  refreshes late was shown 8.3 ms; the excess is at most B less one refresh. Only presentation
  times close that. It under-credits where B is shorter than the cadence, with no floor but
  B's own 4 ms: after one 1/240 s interval (a 240 Hz monitor the window left, or a burst of
  callbacks) 60 draws a second earn 4.17 ms of each 16.7 ms, a quarter, and 30 draws a second
  4.17 ms of each 33.3 ms, an eighth, so the 2 s cap on the fade is 16 s on the clock there,
  until the visualizer is mounted again. Measuring the cadence cannot safely raise the credit:
  that needs the limiter to take a correction for a frame after its successor is drawn, or the
  platform to report presentation times. Modelled at half of each interval and the default 3 Hz: a dark frame is black at once and starts
  about 0.25 s after the change; a frame with luma to drop is held about 0.7 s (two luma tokens,
  0.33 s credited), is black about 0.25 s and rises about 0.7 s after the start; a full-frame
  saturated red is held about 2.7 s (the red token, 1.33 s credited) and rises about 2.7 s after
  the start. The 2 s cap on the fade is about 4 s on the clock. It is reached if the grant word
  never clears, and also if the word clears and no read of it finishes. It is later than the
  soonest a fade may end without such a read (Oscillux `frame-limiter`: the red token's refill,
  1.33 s at 3 Hz, and two frames more), so a fade that ends at the cap has met that too.
- After a change of device pixel ratio the cached CSS box says nothing about the slot's physical
  pixels. Until the slot is observed at the new ratio the frame on screen is held undrawn: a
  zoom freezes the visual for a frame or two, three at most. The three-frame limit holds for a
  slot with no size too: on the fourth frame it is measured as empty, the cached device-pixel
  box is dropped, and drawing goes on, which fades the frame to black. A ratio that is left and
  come back to, or met again after a lost context, is observed afresh and waits its frames
  again.
- The grant word is read through a pixel-pack buffer and a fence, never by a blocking
  `readPixels`. Each frame of the fade reads its own grant after everything else it issues, in
  place of a read the GPU had not finished, and the next frame takes the word, before it issues
  anything, only if the fence has passed: the fade ends a frame after the grant, and at the 2 s
  cap on a GPU that runs more than a frame behind. The residual: `getBufferSubData` is still a
  round trip to the browser's GPU process, so commands issued between frames (an artwork
  upload, a visual's compile) or by other contexts can delay that one call.
- A frame whose slot shrank keeps its size until it has faded, up to the fade's cap, and lies
  over whatever the slot's neighbours show there; the audio stage no longer clips its children
  while the visualizer is on, so the page can gain scrollable overflow for that long. This is
  accepted: the alternative crops lit pixels with no judged draw. The window's own edge still
  crops the canvas, which no page can prevent.
- A slot that shrinks and grows back between two draws costs nothing: no draw asked for another
  geometry, and the frame's pixels never left view.

## Acceptance evidence

- vitest: `renderer.test.ts` (fallback order, probe cleanup, the one path for a geometry change),
  `Visualizer.context.test.tsx` (loss, restore, failed restore, listener removal),
  `Visualizer.test.tsx` (`backingSize`, `chosenVisual`, `viewClip`, the black slot), `cadence.test.ts` (synthetic 30 Hz
  clock through the vendored Oscillux governor).

## Rollback

Revert the change. No schema, wire or stored state is touched.
