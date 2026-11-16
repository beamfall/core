# web-audio-visualizer Specification

## Purpose
Draw a pinned Oscillux visual behind the Web Viewer audio stage that reacts to the playing track, takes the track's artwork as its image input, and never interrupts playback.

## Requirements

### Requirement: The visual's image input is the playing track's artwork

The Web Viewer visualizer SHALL supply the playing audio item's artwork as the Oscillux visual's
image input when the artwork is served from the page's origin and decodes. It SHALL supply a
neutral stand-in image when the item has no artwork, the artwork is cross-origin, or loading or
decoding fails, and the visual SHALL keep drawing in every case.

#### Scenario: Track with artwork

- **WHEN** an audio item with same-origin artwork plays with an image-input visual
- **THEN** the visual is drawn with that artwork as its image input

#### Scenario: Next track

- **WHEN** playback advances to an item with different artwork
- **THEN** the image input changes to the new item's artwork

#### Scenario: No usable artwork

- **WHEN** the item has no artwork, or its artwork is cross-origin or fails to load
- **THEN** the visual draws with the neutral stand-in and playback is unaffected

### Requirement: The visualizer blends between visuals with the Oscillux switch pass

When the Web Viewer visualizer changes from one Oscillux visual to another, it SHALL blend the
outgoing visual into the incoming one through the switch pass vendored unchanged from the pinned
Oscillux release, with the progress timed by the Oscillux transition scheduler, instead of
replacing the visual in one frame. Core SHALL NOT supply its own transition shader. Under
`prefers-reduced-motion` the visual SHALL change at once.

#### Scenario: Album change

- **WHEN** playback advances to an item whose artwork selects a different visual
- **THEN** both visuals draw while the switch pass blends from the outgoing one to the incoming
  one, and the outgoing visual is dropped once the switch ends

#### Scenario: Same artwork

- **WHEN** playback advances to an item that selects the same visual
- **THEN** the visual keeps drawing with no switch

#### Scenario: Reduced motion

- **WHEN** the visual changes while the user prefers reduced motion
- **THEN** the incoming visual replaces the outgoing one at once

#### Scenario: Change during a switch

- **WHEN** another visual is requested while a switch is running
- **THEN** it starts once the running switch ends
