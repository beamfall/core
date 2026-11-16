## ADDED Requirements

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
