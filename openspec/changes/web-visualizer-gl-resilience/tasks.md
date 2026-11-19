## 1. Feedback format (CORE-2481)

- [x] 1.1 `pickFeedbackFormat`: float, half-float, RGBA8, each probed on a real attachment
- [x] 1.2 Expose the chosen format as `Renderer.feedbackLabel`
- [x] 1.3 vitest for the fallback order and probe cleanup

## 2. Context loss (CORE-2482)

- [x] 2.1 Suspend and `preventDefault()` on `webglcontextlost`
- [x] 2.2 Rebuild the renderer, visual and artwork on `webglcontextrestored`; failure falls to the failed state
- [x] 2.3 Remove listeners on unmount
- [x] 2.4 vitest simulating loss and restore

## 3. Resize and reduced motion (CORE-2516, CORE-2517)

- [x] 3.1 Same-scale resize: copy the texels in view, start the rest tracked black, write the grant spent
- [x] 3.2 `ResizeObserver` with `device-pixel-content-box`; `backingSize`
- [x] 3.3 Calm fallback for pinned and per-key visuals (`chosenVisual`)
- [x] 3.4 Bind the previous tracker state as `uTrack` for the limiter `rows` pass
- [x] 3.5 Cadence gate before the governor (CORE-2483)
- [x] 3.6 vitest
- [x] 3.7 Grant state starts spent at the first size; `settled` is false on a host-written grant
- [x] 3.8 A spend during the scale-change fade starts the fade again
- [x] 3.9 Hold, fade and restart when a slot grows past a cut texel; clip the canvas to the judged view (`viewClip`)

## 4. Gate and archive

- [ ] 4.1 Land through the core gate
- [ ] 4.2 Archive the change
