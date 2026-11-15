# web-player-overlay Specification

## Purpose
Keep the web player's controls out of the picture's way: they sit on a bottom gradient scrim over the video, secondary options live in one settings sheet, and the controls hide when idle, respecting reduced-motion and reduced-transparency preferences.

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

### Requirement: Player shortcuts never hijack typing

The web player SHALL answer its keyboard shortcuts only while focus is not in a control, text
field, menu or dialog, and SHALL leave keys pressed with Ctrl, Alt or Meta to the browser.

#### Scenario: Typing is never hijacked

- **WHEN** focus is in a text field, a select or an open dialog and the viewer presses L or 5
- **THEN** the player does not seek and the key reaches the focused control

### Requirement: Seek shortcuts

Space or K SHALL play and pause; J and L SHALL jump 10 seconds back and forward; the left and right
arrows SHALL skip by the viewer's skip interval; the digit keys 0 to 9 SHALL seek to 0% to 90% of
the duration. Up and down arrows, M and F keep changing volume, muting and fullscreen.

#### Scenario: Jump with J and L

- **WHEN** a viewer presses L during playback at 30 s
- **THEN** playback moves to 40 s, and J moves it back to 30 s

#### Scenario: Number keys seek by percent

- **WHEN** a viewer presses 5 on a 200 s Media
- **THEN** playback moves to 100 s

### Requirement: Speed and frame step shortcuts

`<` and `>` SHALL step the playback speed down and up through the speed menu's values. Comma and
period SHALL step one frame back and forward while paused, and do nothing while playing. The frame
interval SHALL be measured from the frames the browser presents during playback; where it cannot
be measured the step is 1/30 s.

#### Scenario: Speed up

- **WHEN** a viewer presses `>` at normal speed
- **THEN** playback runs at the next speed in the menu, 1.25×

#### Scenario: Frame step only while paused

- **WHEN** a viewer presses period while paused
- **THEN** playback advances by one frame, and the same key while playing changes nothing

### Requirement: Track shortcuts

C SHALL turn subtitles off, or back on to the last shown subtitle track (the first one when none
was shown); S SHALL select the next subtitle track, wrapping through off; A SHALL select the next
audio track.

#### Scenario: Toggle captions

- **WHEN** subtitles show the second track and the viewer presses C twice
- **THEN** subtitles turn off and then come back on the second track

### Requirement: Chapter and queue shortcuts

Page Up and Page Down SHALL go to the previous and next chapter. Shift+P and Shift+N SHALL play the
previous and next queue item when the player has a queue.

#### Scenario: Next chapter

- **WHEN** a viewer presses Page Down before a chapter that starts at 120 s
- **THEN** playback moves to 120 s

### Requirement: Keyboard shortcut help sheet

Pressing `?` in the player SHALL open a dialog listing every player shortcut with its keys and a
description, built from the same table the key handler uses. Escape SHALL close it, and the player
chrome SHALL stay visible while it is open.

#### Scenario: Open and close the sheet

- **WHEN** a viewer presses `?` during playback
- **THEN** a "Keyboard shortcuts" dialog lists the shortcuts, and Escape closes it
