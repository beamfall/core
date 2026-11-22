## ADDED Requirements

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
Playing next card with the next title and the seconds left. The card SHALL NOT change when or
whether playback advances. Seeking out of the final 12 seconds SHALL hide it, and Hide SHALL
dismiss it for the rest of that item.

#### Scenario: Track nears its end

- **WHEN** an audio item with a next item reaches 10 seconds from its end
- **THEN** the Playing next card shows the next title and "10"

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
