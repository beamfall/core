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
