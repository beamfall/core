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
the physical pixels per texel, or the whole texels the slot shows), or the frame's view was
narrowed below the texels the slot shows now, the web visualizer SHALL fade
that frame to black through the flash limiter, then present black for 0.1 s, then start a frame
at the geometry asked for then. No output or tracker texel SHALL be carried from one
geometry to another.

#### Scenario: Window resized during a strobe

- **WHEN** the canvas is resized while the limiter is holding
- **THEN** the frame on screen fades to black at its old size, black is presented, and one frame
  starts at the new size, black and tracked black, with every budget spent

#### Scenario: Display scale drops

- **WHEN** the display scale drops from 3 to 2 physical pixels per texel, or from 2 to 1
- **THEN** the frame fades at its own scale and a frame starts at the new scale from black, with
  no texel copied

#### Scenario: Slot shrank and grew back between two draws

- **WHEN** the slot is observed 400 pixels wide and then 800 again between two draws of an
  800-pixel frame
- **THEN** the view stays 400 pixels wide and the next draw starts the fade, so the hidden half
  comes back with the frame that starts

#### Scenario: Slot with no whole texel

- **WHEN** the slot shrinks to less than one texel
- **THEN** the frame fades to black, and black is presented with no frame until the slot shows a
  whole texel

### Requirement: The fade is granted by the limiter

For the fade the web visualizer SHALL keep the frame's geometry, write the grant state with every
budget spent once, set the area limit to zero and feed the limiter black, so every fall to black
waits for a grant. It SHALL end the fade at the first frame that has read no direction held in
one of the fade's frames, and after 2 s at most. It SHALL count the fade and the black in the
time the limiter is credited with, at most 1/6 s a frame, and the black from the first black
frame shown.

#### Scenario: Fade starts

- **WHEN** a geometry change starts the fade
- **THEN** the grant state is written with every budget spent, the area limit is zero, and the
  limiter's input is black from that frame on

#### Scenario: Slow frame during the fade

- **WHEN** a frame of the fade was shown for one second
- **THEN** the fade counts 1/6 s for it, as the limiter's refill does

#### Scenario: Dark frame

- **WHEN** the fade starts on a frame whose trackers hold no fall of a step to black (no luma
  extreme or reference a step above black, no red one)
- **THEN** the fade's first frame holds no direction, and black is presented at the first frame
  that has read its grant

#### Scenario: Dark frame after a brighter one

- **WHEN** the fade starts on a dark frame whose trackers still hold a peak a step above black
- **THEN** the fall to black is a transition and the fade waits for its grant as for any other
  frame

#### Scenario: Frame with a fall to grant

- **WHEN** the fade's frames hold the fall to black
- **THEN** the fade goes on until a frame has read the grant of the frame that granted it, and
  the grant state the fade wrote, or one read before the fade started, is not taken as a grant

#### Scenario: Grant that never clears

- **WHEN** every frame of the fade holds a direction
- **THEN** black is presented once the fade has counted 2 s

#### Scenario: Steady 60 frames a second

- **WHEN** frames have arrived every 1/60 s since the visualizer mounted
- **THEN** each frame is credited half its interval, so the 0.1 s of black lasts about 0.2 s on
  the clock and the 2 s cap about 4 s

### Requirement: The grant is read without waiting on the GPU

The web visualizer SHALL read a frame's grant through a pixel-pack buffer and a fence it polls
once a frame, and SHALL NOT block on the GPU for it. It SHALL drop a read still in flight at a
later draw outside a fade, at a fade's start and at a withdrawal.

#### Scenario: Grant read while the GPU is busy

- **WHEN** the read of a fade frame's grant has not finished at the next frame
- **THEN** that frame is drawn without waiting and reads its own grant, and the fade ends at
  the first frame that finds the read of the frame before it finished with no direction held

### Requirement: A grant is taken behind none of the renderer's later frames

Each frame of a fade SHALL start the read of its grant after every other command of the frame.
The next frame SHALL take the word, before it issues a command, only if the read's fence has
passed, and SHALL otherwise drop that read for its own.

#### Scenario: Word taken behind no later frame

- **WHEN** a frame of the fade takes a finished read
- **THEN** the read's fence was the last command of the frame before, and no command of a later
  frame was issued ahead of the pack buffer read

#### Scenario: No read ever finishes

- **WHEN** no read of a grant finishes during the fade
- **THEN** black is presented once the fade has counted 2 s

### Requirement: A frame is credited no more than it was shown

The time the web visualizer credits the limiter for a frame SHALL be a lower bound on the time
the frame was on screen. With B the shortest draw interval since the visualizer mounted, clamped
to 4 ms and 1/24 s, the credit for an interval SHALL be `min(B, max(interval / 2, interval - B))`.
B SHALL NOT rise while the visualizer is mounted. A steady cadence SHALL NOT raise the credit
above half the interval: the animation-frame cadence is not the display's refresh period.

#### Scenario: Animation frames at 60 a second on a 120 Hz display

- **WHEN** frames arrive every 16.7 ms and one is painted a refresh late and the next on time
- **THEN** the frame was shown 8.3 ms and is credited 8.3 ms

#### Scenario: Cadence slows from 240 to 60 frames a second

- **WHEN** frames arrived every 1/240 s and then arrive every 1/60 s
- **THEN** each frame is credited 1/240 s for as long as the visualizer is mounted

#### Scenario: Page slows to 30 draws a second on a 120 Hz display

- **WHEN** frames arrived every 1/120 s, then arrive every 1/30 s, and one is painted three
  refreshes late and the next on time
- **THEN** the frame was shown 8.3 ms and is credited 8.3 ms

#### Scenario: Long interval

- **WHEN** an interval of 2 s follows steady 59.94 Hz frames
- **THEN** it is credited 1/59.94 s, one refresh

### Requirement: A change during the fade only moves the target

A geometry change during the fade or the black SHALL change only the geometry the next frame
starts at. It SHALL NOT reallocate, write the grant state or restart either clock. A frame SHALL
start only once two draws running ask for one geometry, and a started frame withdrawn before
its paint SHALL be followed by the black again, so a slot that keeps changing allocates the
targets once in the black's length at most.

#### Scenario: Slot dragged through the fade

- **WHEN** the slot's size changes on every frame of a fade
- **THEN** the fade keeps its geometry and its clock, the grant state is not written again, and
  one frame starts at the last size asked for

#### Scenario: Slot dragged past the black

- **WHEN** the slot's size changes on every frame after the black has run
- **THEN** the canvas stays black and no target is allocated until two draws running ask for one
  size

### Requirement: A frame starts black with every budget spent

Every frame the web visualizer starts (the first, the first after a restored context, and the one
after a fade) SHALL start with its output black, its trackers black and its grant state written
with every budget spent.

#### Scenario: First frame

- **WHEN** the renderer draws its first frame
- **THEN** the grant state it reads has every budget spent

### Requirement: A spent grant state is judged before it is trusted

A grant state the host wrote spent has judged no frame. A paused player SHALL draw a frame through
it before holding the picture, and SHALL keep drawing through a fade and the black after it. It
SHALL hold the picture undrawn while the grant of the frame on screen is being read, and draw
again when the read finds a direction held.

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

#### Scenario: Slot shrinks, then the device pixel ratio changes

- **WHEN** a frame with 800 pixels in view at ratio 1 has its slot observed 400 pixels wide, and
  the ratio then goes to 2 before the next draw
- **THEN** the view is 400 pixels wide from the observation on, while the frame is held and after
  the slot is observed 800 physical pixels wide at the new ratio

#### Scenario: Slot shrinks and grows back during the fade

- **WHEN** a slot at two physical pixels per texel shrinks from 4 to 3 pixels wide and grows back
  to 4
- **THEN** the fourth pixel column stays clipped through the fade and comes back black with the
  frame that starts

### Requirement: A frame judged for a changed slot is withdrawn

A frame judged for a slot that changed before the frame was painted SHALL be withdrawn: the
previous output is presented again and the limiter's state goes back to before the frame. Black
and the frames of a fade SHALL stand. A withdrawn frame that started at a new geometry SHALL
leave black and no frame, so the next draw starts at the geometry asked for then.

#### Scenario: Slot changed before paint

- **WHEN** the slot's size changes between a frame's draw and its paint
- **THEN** the frame is withdrawn, the previous output is presented, and the next draw starts the
  fade from it

#### Scenario: Slot changed during the fade

- **WHEN** the slot's size changes between the draw and the paint of a frame of the fade
- **THEN** that frame stands and the fade's clock is not put back

#### Scenario: Slot changed as the black ended

- **WHEN** the slot's size changes between the draw and the paint of the frame that started
  after the black
- **THEN** that frame is withdrawn, black is presented with no frame, and the next draw starts
  at the new geometry with no second fade

### Requirement: A slot has changed when its geometry has

The slot SHALL count as changed for a withdrawal when the geometry a frame would be drawn at
differs: the backing size, the pixels per texel or the whole texels in view. A change of the
slot's pixels that leaves that geometry as it was SHALL NOT withdraw a frame.

#### Scenario: Slot gains and loses a pixel no texel fills

- **WHEN** at 2 physical pixels per texel the slot alternates between 128 and 129 pixels wide
  before each paint, 64 texels either way
- **THEN** no frame is withdrawn and the limiter's clock advances with every frame

### Requirement: A slot measured at another device pixel ratio is not drawn for

When the device pixel ratio differs from the one the slot was last observed at, the web
visualizer SHALL NOT draw, start a fade or move the clip from the cached CSS size. It SHALL leave
the frame on screen as it is, observe the slot afresh, and draw from the next observation. An
observation SHALL narrow the view of the frame on screen when it is made, whatever the ratio, and
nothing on this path SHALL widen it.

#### Scenario: Zoom with the slot's pixels unchanged

- **WHEN** a slot of 800 by 600 physical pixels at ratio 2 is zoomed to ratio 1 and the next
  animation frame runs before the slot is observed
- **THEN** nothing is drawn, no fade starts and the clip does not shrink; after the observation
  reports 800 by 600 the frame goes on with no fade

### Requirement: A ratio change is observed afresh

After a change of device pixel ratio the web visualizer SHALL stop observing the slot and observe
it again, since observing a slot already observed reports nothing while its box is unchanged. If
no observation has come after three animation frames, the device-pixel box last observed is
still the slot's, and it SHALL draw from that box and the CSS size read from layout.

#### Scenario: Zoom the observer in place does not report

- **WHEN** a zoom leaves the slot's device-pixel box unchanged, so the observation in place
  reports nothing
- **THEN** the fresh observation reports the slot before the paint and the next frame is drawn

#### Scenario: No observation at the new ratio

- **WHEN** the ratio changes and three animation frames pass with no observation
- **THEN** the fourth draws from the device-pixel box last observed, with the pixels per texel of
  the new ratio

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
