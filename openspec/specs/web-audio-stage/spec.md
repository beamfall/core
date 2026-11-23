# web-audio-stage Specification

## Purpose
Show the playing audio item in the Web Viewer on a stage that holds its artwork, title and credits, carries the item across track changes without flashing empty, and fits every window size.

## Requirements

### Requirement: Track changes on the audio stage transition smoothly

When the playing audio item changes, the Web Viewer audio stage SHALL transition its artwork,
backdrop and words instead of replacing them in place. Incoming artwork SHALL appear only once it
has loaded and decoded, with the outgoing artwork visible until then. Outgoing words SHALL leave
while incoming words arrive, without growing the card. Under `prefers-reduced-motion` the stage
SHALL crossfade only.

#### Scenario: Next item with artwork

- **WHEN** playback advances to an item whose artwork has not loaded yet
- **THEN** the previous artwork stays visible until the new artwork loads and decodes, then the new
  artwork animates in over it and the previous artwork is removed

#### Scenario: Next item without artwork

- **WHEN** playback advances to an item with no usable artwork
- **THEN** the previous artwork transitions to the placeholder in the same style

#### Scenario: Words change

- **WHEN** playback advances to another item
- **THEN** the previous title and credits animate out while the new ones animate in, and only the
  new ones are exposed to assistive technology

#### Scenario: Reduced motion

- **WHEN** the viewer prefers reduced motion
- **THEN** the artwork and words crossfade without moving, scaling or blurring

### Requirement: The visualizer keeps moving across a track change

The audio stage's visualizer SHALL keep drawing while the next item's source loads, without
tearing down its renderer, and SHALL hand over from one visual to the next through the visual's
own feedback rather than a cut to black. It SHALL decode the next item's analysis ahead of the
change and SHALL ease its audio values from one item to the next. Items that share the same
artwork SHALL keep the same visual.

#### Scenario: Advance to a decoded next item

- **WHEN** playback advances to the next item and that item's analysis was decoded ahead
- **THEN** the visual keeps moving through the change and reacts to the new item as it starts

#### Scenario: Source loading

- **WHEN** the next item's source is loading
- **THEN** the visual keeps drawing and its audio values glide toward rest instead of freezing

#### Scenario: Same album

- **WHEN** the next item has the same artwork as the current one
- **THEN** the visual stays the same
- **AND** the artwork does not transition, and no other visual or neutral image shows while the
  next item's details load

### Requirement: The audio stage fits narrow and short windows

On a narrow window the audio stage SHALL fit its artwork to the height the words leave, so the
artwork is not clipped by the top bar or the transport. The transport SHALL NOT repeat the
item's title over the stage, and on a phone-width window its play controls SHALL sit in the
transport row rather than over the card.

#### Scenario: Short phone window with the controls shown

- **WHEN** the audio stage is shown in a window about 414 by 515 pixels and the controls are shown
- **THEN** the artwork, title and credits are fully visible, and no control covers them

#### Scenario: Long title on a short phone window

- **WHEN** the item's title or credits would wrap to many lines on a narrow window
- **THEN** the title keeps to two lines and each credit line to one, and the artwork keeps its
  room

### Requirement: Track-change motion has selectable styles

The audio stage SHALL animate its words on a track change in one of three styles (letter rise with
blur, split-flap roll, masked wipe) and its artwork in one of four (crossfade with scale, flip,
slide over, blur dissolve), animating only transform, opacity and filter. A per-browser setting in
the player settings sheet SHALL fix each to one style or to "Mix", which picks a style per change.

#### Scenario: Fixed style

- **WHEN** the viewer sets the word style to "Split-flap" and the track changes
- **THEN** the new words roll in letter by letter

#### Scenario: Mix

- **WHEN** both settings are "Mix"
- **THEN** each change picks a word style and a matching artwork style

### Requirement: Track-change words arrive in sequence and announce once

The incoming words SHALL arrive after the outgoing words start to leave, in order: title, then the
person credit, then the container credit and queue position, without changing the card's height.
The word block SHALL be a polite live region that announces each new item once, and per-letter
pieces SHALL be hidden from assistive technology.

#### Scenario: Screen reader on a track change

- **WHEN** the track changes while a screen reader is running
- **THEN** the new title and credits are announced once, as whole words

### Requirement: Rapid track changes cancel cleanly

A further track change during a transition SHALL cancel the unfinished one, so at most one
outgoing item is drawn under the incoming one.

#### Scenario: Rapid skips

- **WHEN** the viewer skips three tracks within a second
- **THEN** only the last item arrives, and at most one outgoing item is drawn under it

### Requirement: Previous and Next name their neighbour

When the player knows the previous or next item, the Previous and Next controls SHALL show its
title on hover and keyboard focus, and SHALL include it in their accessible name. The player
SHALL offer an Up next drawer listing the previous item, the current item and the upcoming
items, and activating a row SHALL play that item.

#### Scenario: Hovering Next

- **WHEN** a queue has a next item titled "Blue Room" and the viewer hovers Next
- **THEN** a label reading "Next" and "Blue Room" appears by the control, and its accessible name is "Next: Blue Room"

### Requirement: Audio warns before playing next

During the final 12 seconds of an audio item that has a next item, the audio stage SHALL show a
Playing next card with the next title and the seconds left, and the next item's picture and artist
when its read has them. A picture that fails to load SHALL be left out. Waiting on the card SHALL
NOT change when or whether playback advances. Seeking out of the final 12 seconds SHALL hide it,
and Hide SHALL dismiss it for the rest of that item.

#### Scenario: Track nears its end

- **WHEN** an audio item with a next item reaches 10 seconds from its end
- **THEN** the Playing next card shows the next title and "10"

#### Scenario: Next item has a picture and an artist

- **WHEN** the card shows and the next item's read has a poster and a person credit "Cee"
- **THEN** the card shows that picture and "Cee", and its region is named "Playing next: C by Cee"

### Requirement: Play now crossfades to the next item

Play now on the Playing next card SHALL hide the card at once and move to the next item, fading the
playing item out and the next item in instead of cutting. It SHALL keep any volume the listener
sets during the fade without saving the faded levels.

#### Scenario: Play now

- **WHEN** the listener presses Play now on the card while the item plays
- **THEN** the card hides at once, the playing item fades out, the next item plays and fades in,
  and the audio stage and its visual stay mounted

### Requirement: Ambient mode protects OLED screens

The audio stage SHALL offer an Ambient mode, remembered per browser. In Ambient mode the
backdrop and visual SHALL be black, and after 45 seconds without pointer, key or touch input the
player chrome SHALL hide and the stage SHALL dim and move its card slowly between safe anchors.
Input or a track change SHALL restore full brightness and chrome at once. The drift and the dim
SHALL be computed by Oscillux curves. With reduced motion the card SHALL step between anchors
without continuous motion.

#### Scenario: Idle in Ambient mode

- **WHEN** Ambient mode is on and 45 seconds pass without input
- **THEN** the chrome hides, the stage dims and the card begins to drift

#### Scenario: Input wakes the stage

- **WHEN** the stage is idle in Ambient mode and the pointer moves
- **THEN** the chrome shows and the stage returns to full brightness

### Requirement: Listening controls live in the Settings sheet

On the audio stage, the Speed, Remove long pauses and Sleep timer controls SHALL appear as rows in
the transport's Settings sheet, not on the stage. Their pickers SHALL open within the sheet, and
choosing from them SHALL NOT close it. Radio tuning controls SHALL sit in the player bar beside
Back and SHALL NOT overlap it.

#### Scenario: Opening Settings on the audio stage

- **WHEN** a listener opens Settings while an audio track plays on the audio stage
- **THEN** the sheet lists Playback speed, Remove long pauses and Sleep timer rows, and the stage shows none of them

#### Scenario: Radio station playing

- **WHEN** a radio station plays on the audio stage
- **THEN** its tuning selects appear in the player bar after Back, without overlapping it
