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

### Requirement: One path for every geometry change

When a frame is on screen and the geometry asked for differs from it (the backing size in texels,
the physical pixels per texel, or the whole texels the slot shows), the web visualizer SHALL fade
that frame to black through the flash limiter for 2 s, then present black for 0.1 s, then start a
frame at the geometry asked for then. No output or tracker texel SHALL be carried from one
geometry to another.

#### Scenario: Window resized during a strobe

- **WHEN** the canvas is resized while the limiter is holding
- **THEN** the frame on screen fades to black at its old size, black is presented, and one frame
  starts at the new size, black and tracked black, with every budget spent

#### Scenario: Display scale drops

- **WHEN** the display scale drops from 3 to 2 physical pixels per texel, or from 2 to 1
- **THEN** the frame fades at its own scale and a frame starts at the new scale from black, with
  no texel copied

#### Scenario: Slot with no whole texel

- **WHEN** the slot shrinks to less than one texel
- **THEN** the frame fades to black, and black is presented with no frame until the slot shows a
  whole texel

### Requirement: The fade is granted by the limiter

For the fade the web visualizer SHALL keep the frame's geometry, write the grant state with every
budget spent once, set the area limit to zero and feed the limiter black, so every fall to black
waits for a grant. It SHALL count the fade and the black in the time the limiter is credited
with, at most 1/6 s a frame, and the black from the first black frame shown.

#### Scenario: Fade starts

- **WHEN** a geometry change starts the fade
- **THEN** the grant state is written with every budget spent, the area limit is zero, and the
  limiter's input is black from that frame on

#### Scenario: Slow frame during the fade

- **WHEN** a frame of the fade was shown for one second
- **THEN** the fade counts 1/6 s for it, as the limiter's refill does

### Requirement: A change during the fade only moves the target

A geometry change during the fade or the black SHALL change only the geometry the next frame
starts at. It SHALL NOT reallocate, write the grant state or restart either clock.

#### Scenario: Slot dragged through the fade

- **WHEN** the slot's size changes on every frame of a fade
- **THEN** the fade keeps its geometry and its clock, the grant state is not written again, and
  one frame starts at the last size asked for

### Requirement: A frame starts black with every budget spent

Every frame the web visualizer starts (the first, the first after a restored context, and the one
after a fade) SHALL start with its output black, its trackers black and its grant state written
with every budget spent.

#### Scenario: First frame

- **WHEN** the renderer draws its first frame
- **THEN** the grant state it reads has every budget spent

### Requirement: A spent grant state is judged before it is trusted

A grant state the host wrote spent has judged no frame. A paused player SHALL draw a frame through
it before holding the picture, and SHALL keep drawing through a fade and the black after it.

#### Scenario: Paused on a grant the host wrote

- **WHEN** playback is paused and the grant state was written spent with no frame judged since
- **THEN** the renderer does not report the picture settled, and the next frame is drawn

#### Scenario: Resized while paused

- **WHEN** playback is paused and the slot is resized
- **THEN** the renderer does not report the picture settled until a frame at the new size has
  been judged with no direction held

### Requirement: Only whole judged texels are shown

A frame SHALL present only the whole texels its slot shows when it starts, on black. A texel the
slot cuts SHALL NOT be presented.

#### Scenario: Slot cuts a texel

- **WHEN** a frame starts at two physical pixels per texel in a slot 3 pixels wide
- **THEN** one texel column is presented and the third pixel column shows black

### Requirement: The view of a frame only shrinks

Until the next frame starts, the web visualizer SHALL keep the canvas at the frame's size and
position, clipped to the smallest slot seen since the frame started, so a pixel leaves view only
by that clip and a pixel the slot hid does not come back.

#### Scenario: Slot shrinks and grows back during the fade

- **WHEN** a slot at two physical pixels per texel shrinks from 4 to 3 pixels wide and grows back
  to 4
- **THEN** the fourth pixel column stays clipped through the fade and comes back black with the
  frame that starts

### Requirement: A frame judged for a changed slot is withdrawn

A frame judged for a slot that changed before the frame was painted SHALL be withdrawn: the
previous output is presented again and the limiter's state goes back to before the frame. Black
and the frames of a fade SHALL stand.

#### Scenario: Slot changed before paint

- **WHEN** the slot's size changes between a frame's draw and its paint
- **THEN** the frame is withdrawn, the previous output is presented, and the next draw starts the
  fade from it

#### Scenario: Slot changed during the fade

- **WHEN** the slot's size changes between the draw and the paint of a frame of the fade
- **THEN** that frame stands and the fade's clock is not put back

### Requirement: Unjudged pixels show black

Wherever the slot shows no judged pixel (past the canvas, through its clip, before its first
frame, between a fade and the frame that starts) the web visualizer SHALL show black, the level
those pixels' trackers start at, whatever the theme.

#### Scenario: Strip not yet drawn

- **WHEN** the slot grows and paints before the next frame is drawn
- **THEN** the new strip shows black, not the theme's scrim, and stays black until the frame that
  starts after the fade brings its texels into view from black

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
