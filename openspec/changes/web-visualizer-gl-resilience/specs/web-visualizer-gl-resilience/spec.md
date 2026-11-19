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

### Requirement: A resize never hands out a flash budget

On a canvas resize at the same display scale the web visualizer SHALL rebuild the flash limiter's
size-dependent textures, copy the output and tracker texels that were in view one to one from the
top left, start every other texel black and tracked black, and write the grant state with every
budget spent. The grant state SHALL start spent at the first size too.

#### Scenario: Window resized during a strobe

- **WHEN** the canvas is resized at the same display scale while the limiter is holding
- **THEN** the texels that were in view keep their output and tracker state, new area starts black
  and tracked black, and the grant state is written with every budget spent

#### Scenario: First frame

- **WHEN** the renderer draws its first frame
- **THEN** the grant state it reads has every budget spent

### Requirement: A spent grant state is judged before it is trusted

A grant state the host wrote spent has judged no frame. A paused player SHALL draw a frame through
it before holding the picture, and a spend during the fade of a display-scale change SHALL start
that fade again. A spend outlives a withdrawn frame, so the fade a withdrawn frame goes back to
SHALL start again too.

#### Scenario: Paused on a grant the host wrote

- **WHEN** playback is paused and the grant state was written spent with no frame judged since
- **THEN** the renderer does not report the picture settled, and the next frame is drawn

#### Scenario: Resize during the scale-change fade

- **WHEN** a resize spends the budgets one second into the fade of a display-scale change
- **THEN** the fade runs its full length again from that spend

#### Scenario: Withdrawn frame that cancelled the fade

- **WHEN** a frame cancels a fade with one second left, spends the budgets, and is withdrawn
  before it is painted
- **THEN** the fade it goes back to runs its full length again

### Requirement: Only judged pixels are shown

Physical pixels SHALL come into view only as whole texels that start black, or, from nothing in
view, as one texel cut by a slot under one texel wide. When the slot grows past a texel it cut,
the web visualizer SHALL hold the view, fade the frame to black and start again with nothing in
view. It SHALL clip the canvas to the pixels the limiter has judged in view.

#### Scenario: Slot grows past a cut texel

- **WHEN** a slot at two physical pixels per texel shrinks from 4 to 3 pixels wide, cutting its
  second texel, and grows back to 4
- **THEN** the fourth pixel column stays clipped, the frame fades to black, and the view starts
  again from black

#### Scenario: Slot grows by a whole texel

- **WHEN** a slot showing whole texels grows by one texel
- **THEN** the new texel comes into view black and tracked black, with every budget spent

### Requirement: Unjudged pixels show black

Wherever the slot shows no judged pixel (past the canvas, through its clip, before its first
frame) the web visualizer SHALL show black, the level those pixels' trackers start at, whatever
the theme.

#### Scenario: Strip not yet drawn

- **WHEN** the slot grows and paints before the next frame is drawn
- **THEN** the new strip shows black, not the theme's scrim, and its texels then come into view
  from black

### Requirement: Backing store from device pixels

The web visualizer SHALL size its backing store from the canvas's device-pixel content box where
the browser reports it, with the device pixel ratio capped at 2.

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
