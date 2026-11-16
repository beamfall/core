## ADDED Requirements

### Requirement: Track changes on the audio stage transition smoothly

When the playing audio item changes, the Web Viewer audio stage SHALL transition its artwork,
backdrop and words instead of replacing them in place. The incoming artwork SHALL appear only once
it has loaded, with the outgoing artwork kept visible until then, so the frame does not flash empty
between two items with artwork. The outgoing words SHALL leave while the incoming words arrive,
without growing the card. Under `prefers-reduced-motion` the stage SHALL swap without animation.

#### Scenario: Next item with artwork

- **WHEN** playback advances to an item whose artwork has not loaded yet
- **THEN** the previous artwork stays visible until the new artwork loads, then the new artwork
  animates in over it and the previous artwork is removed

#### Scenario: Next item without artwork

- **WHEN** playback advances to an item with no usable artwork
- **THEN** the previous artwork fades out to the fallback frame

#### Scenario: Words change

- **WHEN** playback advances to another item
- **THEN** the previous title and credits animate out while the new ones animate in, and only the
  new ones are exposed to assistive technology

#### Scenario: Reduced motion

- **WHEN** the viewer prefers reduced motion
- **THEN** the artwork and words swap without animation

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
