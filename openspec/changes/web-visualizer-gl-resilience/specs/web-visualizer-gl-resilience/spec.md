## ADDED Requirements

### Requirement: Feedback format fallback

The web visualizer SHALL choose its feedback format by trying RGBA16F with
`EXT_color_buffer_float`, then RGBA16F with `EXT_color_buffer_half_float`, then RGBA8, accepting
the first whose probe framebuffer is complete, and SHALL delete the probe resources. It SHALL
expose the chosen format for diagnostics.

#### Scenario: Float framebuffer incomplete

- **WHEN** `EXT_color_buffer_float` is enabled but its probe framebuffer is incomplete and the
  half-float one is complete
- **THEN** the renderer uses RGBA16F through `EXT_color_buffer_half_float` and reports `half-float`

#### Scenario: No renderable float format

- **WHEN** neither probe completes
- **THEN** the renderer uses RGBA8 and reports `8-bit`

### Requirement: Recovery from WebGL context loss

On `webglcontextlost` the web visualizer SHALL call `preventDefault()` and stop rendering. On
`webglcontextrestored` it SHALL recreate its GL resources, re-apply the selected visual and
artwork, and resume with fresh feedback and limiter state. If the restore fails it SHALL fall back
to the failed state. It SHALL remove its listeners on unmount.

#### Scenario: Loss and restore

- **WHEN** the context is lost and later restored
- **THEN** rendering stops at the loss and resumes on a new renderer after the restore

#### Scenario: Restore fails

- **WHEN** a renderer cannot be created after the restore
- **THEN** the visualizer slot is left empty

### Requirement: Resize keeps the flash budget

The web visualizer SHALL keep the flash limiter's token, tracker and history state across a canvas
resize, rebuilding only the size-dependent textures, and SHALL size its backing store from the
canvas's device-pixel content box where the browser reports it, with the device pixel ratio capped
at 2.

#### Scenario: Window resized during a strobe

- **WHEN** the canvas is resized while the limiter is holding
- **THEN** the limiter's grant and tracker textures are the ones from before the resize

#### Scenario: Fractional CSS size

- **WHEN** the device-pixel content box is 781 x 439 at DPR 2
- **THEN** the backing store is 781 x 439

### Requirement: Reduced motion picks calm visuals

When the viewer prefers reduced motion, the web visualizer SHALL show only visuals Oscillux marks
calm: a pinned visual that is not calm and the per-key visual are replaced by a calm visual chosen
stably from the key.

#### Scenario: Lively visual pinned

- **WHEN** a non-calm visual is pinned and the viewer prefers reduced motion
- **THEN** a calm visual is shown

### Requirement: A browser frame cap is not overload

The web visualizer SHALL NOT lower its render quality for a steady frame cadence at a 30 Hz
browser cap, and SHALL still lower it for frames slower than the cap or an unsteady cadence.

#### Scenario: Power-saving cap

- **WHEN** frames arrive at a steady 30 Hz for 20 s with light rendering
- **THEN** the governor stays at its top level
