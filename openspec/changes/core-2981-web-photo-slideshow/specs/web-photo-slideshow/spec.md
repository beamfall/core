## ADDED Requirements

### Requirement: Slideshow route and Source

The web client SHALL offer a full-screen slideshow at `/slideshow` whose Source is an ordered
Collection or a Source kind (`all-items`, `favorites`, `on-this-day`) scoped to one Library or to
every Library the session can see. A setup sheet SHALL let the viewer pick the Source, the
interval, the Motion, the overlay parts and Dark Room. An ordered Collection's page and a Library's
browse page SHALL offer a Slideshow action when their listed items include an image.

#### Scenario: Slideshow from a Collection

- **WHEN** a viewer chooses Slideshow on an ordered Collection's page that lists images
- **THEN** the address becomes `/slideshow` naming that Collection and its images play in order

#### Scenario: No Source chosen

- **WHEN** a viewer opens `/slideshow` without a Source
- **THEN** the setup sheet opens with the Source picker

### Requirement: Slide timing

Each slide SHALL stay on screen for the chosen interval, from 5 to 60 seconds with a default of
12, measured from when the slide was painted, within ±0.5 seconds. The client SHALL preload the
next image while the current slide shows. Pausing SHALL hold the current slide; resuming SHALL
keep the time already shown.

#### Scenario: Interval clamped

- **WHEN** a slideshow is opened with an interval outside 5 to 60 seconds
- **THEN** the interval used is the nearest bound

### Requirement: Slides and clips

The slideshow SHALL show an image's poster derivative. A clip SHALL play muted and inline from its
generated preview clip for the slide's interval, and a clip without one SHALL show its poster.
Clips SHALL be included only from a Source that also lists images.

#### Scenario: Clip plays muted

- **WHEN** the current slide is a clip with a preview clip
- **THEN** it plays inline with sound muted

### Requirement: Household isolation

The slideshow SHALL show only items the server lists for the acting Profile and Reveal state. It
SHALL drop locked items and the Profile's hidden items, and SHALL reload its slides when the viewer
identity changes.

#### Scenario: Hidden item never shown

- **WHEN** a Source lists an item the acting Profile has hidden
- **THEN** that item never appears as a slide

### Requirement: Engagement silence

Watching a slideshow SHALL NOT mark anything played or viewed. The slideshow SHALL NOT request a
playback decision, open a playback session or send progress for a slide.

#### Scenario: No playback session for slides

- **WHEN** a slideshow shows images and clips
- **THEN** no `/api/playback-info` request and no playback progress or end is sent

### Requirement: Overlay and music

The slideshow SHALL offer an overlay with a clock, a caption with the item's date and subtitle when
it has them, and the title of the track the current play queue is playing. Audio the player is
playing SHALL keep playing under the slideshow.

#### Scenario: Now playing

- **WHEN** the play queue is playing a track and the overlay is on
- **THEN** the overlay names that track

### Requirement: Dark Room schedule

The setup sheet SHALL offer Dark Room as an on/off setting with a start and an end time, defaulting
to 22:00 and 07:00 and stored per device. A window whose end is earlier than its start SHALL cross
midnight. Inside the window, while Dark Room is on, the stage SHALL be dimmed.

#### Scenario: Window crosses midnight

- **WHEN** Dark Room is on from 22:00 to 07:00 and the local time is 02:30
- **THEN** the stage is dimmed

#### Scenario: Outside the window

- **WHEN** Dark Room is on from 22:00 to 07:00 and the local time is 12:00
- **THEN** the stage is not dimmed

### Requirement: Dark Room dimming is UI only

The Dark Room dim SHALL be a plain opaque black scrim at about 85% over the stage, with no filter,
blur or shader. The clock and caption SHALL stay readable but dim. D SHALL dim or undim at once
until the schedule next changes state.

#### Scenario: Dim now

- **WHEN** the stage is not dimmed and the viewer presses D
- **THEN** the scrim covers the stage until the schedule next changes state

### Requirement: Portrait pairing

On a landscape screen, a portrait image SHALL share the stage with the next slide when that slide is
also a portrait image, side by side in two halves with a small gap, each fitted whole. Otherwise a
portrait SHALL show alone. Clips SHALL never pair.

#### Scenario: Two portraits pair

- **WHEN** the current and next slides are portrait images on a landscape screen
- **THEN** both show side by side

#### Scenario: Lone portrait

- **WHEN** a portrait image is followed by a landscape image, or ends the queue
- **THEN** the portrait shows alone

### Requirement: A pair is one slide

A pair SHALL be one slide for timing and for Previous and Next, and its caption SHALL be the first
image's date and place.

#### Scenario: Next skips past a pair

- **WHEN** a pair is showing and the viewer presses Next
- **THEN** the slide after the pair's second image shows

### Requirement: Pairing is layout only

Pairing SHALL add no visual effect. A lone portrait SHALL keep dark bars until the Oscillux `fill`
and `pair` layouts (CORE-2980) are wired through the motion seam, since a blurred fill is a visual
effect and visual effects come only from Oscillux.

#### Scenario: No blurred fill

- **WHEN** a lone portrait shows and the pinned Oscillux release has no fill layout
- **THEN** the space beside it is the dark stage, with no blur of the photo

### Requirement: Keyboard control

Space SHALL pause and resume, the left and right arrows SHALL step, O SHALL toggle the overlay, D
SHALL toggle Dark Room, F SHALL toggle browser full screen and S SHALL open the setup sheet.
Escape SHALL close the setup sheet when it is open, and otherwise close the slideshow.

#### Scenario: Escape closes

- **WHEN** the setup sheet is closed and the viewer presses Escape
- **THEN** the slideshow closes and the viewer returns to where it was opened

### Requirement: Motion comes only from Oscillux

Slide transitions and motion SHALL come only from Oscillux. While the pinned Oscillux release has
no photo-slideshow mode, slides SHALL change with a hard cut and the Calm and Visualiser choices
SHALL be shown as unavailable.

#### Scenario: Hard cut without an Oscillux slideshow mode

- **WHEN** the pinned Oscillux release offers no photo-slideshow mode
- **THEN** the next slide replaces the current one with no blend
