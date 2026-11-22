## ADDED Requirements

### Requirement: Lyrics view on the audio stage

The web player SHALL offer a Lyrics toggle while the audio stage shows. Turned on, the stage SHALL
show the artwork on the left and the item's `mediaLyrics` on the right. Synced lyrics SHALL
highlight the line at the playback position, dim the other lines and keep the current line in view;
activating a synced line SHALL seek playback to its start. Plain lyrics SHALL show as text. An item
without lyrics, or a failed read, SHALL show a quiet "No lyrics".

#### Scenario: Synced lyrics follow playback

- **WHEN** the Lyrics view is open and playback passes a synced line's start time
- **THEN** that line becomes the current line and is scrolled into view

#### Scenario: Click to seek

- **WHEN** the viewer activates a synced line
- **THEN** playback seeks to that line's start time

#### Scenario: Track without lyrics

- **WHEN** the Lyrics view is open on an item whose `mediaLyrics` has no lines
- **THEN** the view shows "No lyrics" and nothing else
