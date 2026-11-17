## MODIFIED Requirements

### Requirement: Track changes on the audio stage transition smoothly

When the playing audio item changes, the Web Viewer audio stage SHALL transition its artwork,
backdrop and words instead of replacing them in place. The incoming artwork SHALL appear only once
it has loaded and decoded, with the outgoing artwork kept visible until then, so the frame does not
flash empty between two items with artwork. The outgoing words SHALL leave while the incoming words
arrive, without growing the card. Under `prefers-reduced-motion` the stage SHALL crossfade only,
with no movement, scaling or blur.

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

## ADDED Requirements

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
