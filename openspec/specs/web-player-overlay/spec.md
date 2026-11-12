# web-player-overlay Specification

## Purpose
TBD - created by archiving change web-player-overlay. Update Purpose after archive.

## Requirements

### Requirement: Scrim overlay instead of a card

While media plays, the web player SHALL draw its controls on a translucent gradient scrim rising
from the bottom of the video stage rather than on an opaque card. The scrim SHALL become solid when
the viewer prefers reduced transparency.

#### Scenario: Playing overlay

- **WHEN** a viewer starts playback and moves the pointer over the stage
- **THEN** the title, source line, scrubber and one row of controls appear over a bottom gradient
  with the picture visible behind them

### Requirement: Settings sheet holds secondary playback options

Playback speed, audio track, subtitle track, subtitle styling, quality, the per-show skip interval
and playback details SHALL be reachable from one settings control in the transport row. Each option
SHALL show its current value. The settings surface SHALL close on Escape and return focus to the
settings control. At phone width it SHALL present as a bottom sheet.

#### Scenario: Change audio track from settings

- **WHEN** a viewer opens settings and chooses the audio row
- **THEN** the audio listbox opens with the current track selected, and choosing another track
  switches audio

#### Scenario: Escape closes settings

- **WHEN** settings is open and the viewer presses Escape
- **THEN** settings closes and focus returns to the settings control

### Requirement: Slim scrubber with time feedback

The scrubber SHALL be a thin track that grows on hover and keyboard focus. It SHALL show the
buffered range, chapter and segment ticks, elapsed and remaining time, and a time tooltip at the
pointer position. It SHALL stay a labelled slider operable with the arrow keys.

#### Scenario: Hover preview

- **WHEN** a pointer hovers the scrubber at 40% of a 100-minute film
- **THEN** a tooltip shows 40:00 at the pointer, above the trickplay frame when one exists

### Requirement: Slide-out volume

Volume SHALL be an icon button that toggles mute, with a volume slider that slides out on hover or
keyboard focus and keeps its accessible name and value text.

#### Scenario: Keyboard volume

- **WHEN** a keyboard user tabs to the volume slider
- **THEN** the slider is visible and arrow keys change the volume

### Requirement: Idle hide and reduced motion

The overlay SHALL fade out after idle playback and reappear on pointer movement, key press or focus.
It SHALL NOT hide while paused or while settings or a menu is open. With reduced motion the overlay,
scrubber and settings transitions SHALL be instant.

#### Scenario: Settings open while idle

- **WHEN** settings is open during playback and the viewer stops moving the pointer
- **THEN** the overlay stays visible until settings closes
