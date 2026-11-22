# web-photo-slideshow Specification

## Purpose
Play a photo slideshow in the web client from a Source, with slide timing and support for both photos and clips.

## Requirements

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

### Requirement: Overlay, music and Dark Room

The slideshow SHALL offer an overlay with a clock, a caption with the item's date and subtitle when
it has them, and the title of the track the current play queue is playing. Audio the player is
playing SHALL keep playing under the slideshow. Dark Room SHALL dim the stage to near-black.

#### Scenario: Now playing

- **WHEN** the play queue is playing a track and the overlay is on
- **THEN** the overlay names that track

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
