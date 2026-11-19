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

- [x] 3.1 One path for every geometry change: fade through the limiter, black, start at the geometry asked for then
- [x] 3.2 `ResizeObserver` with `device-pixel-content-box`; `backingSize`
- [x] 3.3 Calm fallback for pinned and per-key visuals (`chosenVisual`)
- [x] 3.4 Bind the previous tracker state as `uTrack` for the limiter `rows` pass
- [x] 3.5 Cadence gate before the governor (CORE-2483)
- [x] 3.6 vitest
- [x] 3.7 Grant state starts spent at the first size; `settled` is false on a host-written grant
- [x] 3.8 A geometry change during the fade or the black only moves the target: no reallocation, spend or restart
- [x] 3.9 Present whole texels in view only; keep the canvas and clip it to the smallest slot until the start (`viewClip`)
- [x] 3.10 Paint the slot behind the canvas black (`TRACKED_BLACK`), not the theme's scrim
- [x] 3.11 Withdraw only a judged frame outside a fade; fade and black frames stand
- [x] 3.12 Delete the copy paths (divisor, overlap, partial expose), the cut-texel hold and `collapse`
- [x] 3.13 End the fade at the frame after one whose grant held no direction; 2 s is the cap
- [x] 3.14 Compare the slot by the geometry a frame is drawn at (`geometryKey`), not by its pixels
- [x] 3.15 A withdrawn frame that started leaves black and no frame, with no second fade
- [x] 3.16 Hold the frame after a device-pixel-ratio change until the slot is observed again
- [x] 3.17 Narrow the view at each observation (`narrow`), before the device-pixel-ratio guard; fade a frame whose view was cut
- [x] 3.18 Observe the slot afresh after a ratio change (unobserve, observe); draw from the last device-pixel box after three frames with no report
- [x] 3.19 Read the grant word through a pixel-pack buffer and a fence, with no blocking `readPixels`
- [x] 3.20 Bound the refresh period by the shortest draw interval since mount (`RefreshBound`); a bound over recent draws over-credits a late frame once the page slows
- [x] 3.21 Decline a credit above half the interval at a steady cadence: animation frames do not give the display's refresh period
- [x] 3.22 Clip the canvas to a narrowed slot in the observation that reports it, before the paint
- [x] 3.23 End a wait for a ratio's report at any report, at a ratio back at the reported one and at a lost context, so a ratio met again is observed afresh
- [x] 3.24 Start a frame only once two draws running ask for one geometry; a withdrawn start costs the black again; check framebuffers at the first start only
- [x] 3.25 Read each fade frame's grant after its last command and take the word at the next frame only if the fence has passed
- [x] 3.26 Keep the drawn geometry as numbers; build a geometry key only for an observation that follows a draw

## 4. Gate and archive

- [ ] 4.1 Land through the core gate
- [ ] 4.2 Archive the change
