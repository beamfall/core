## ADDED Requirements

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
