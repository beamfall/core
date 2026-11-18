## ADDED Requirements

### Requirement: Track pickers before playback

The web Media detail page SHALL show a Version picker when the item has two or more Versions, an
Audio picker when the chosen Version has two or more audio tracks, and a Subtitles picker with an
Off choice when it has caption tracks. A Version pick SHALL be remembered for the profile the way
the player remembers it. The pickers SHALL be hidden when the listing fails.

#### Scenario: Single-track item

- **WHEN** the item has one Version, one audio track and no captions
- **THEN** the page shows no pickers

### Requirement: Picked tracks carry into playback

Starting playback from the Media detail page SHALL carry the audio pick to `playbackInfo` as
`selectedAudioIndex`. The subtitle pick SHALL be shown when the stream attaches, and a subtitle that
must be burned in SHALL be asked for with `selectedSubtitleIndex`. A pick that matches no track at
playback start SHALL be ignored in favour of the server's choice.

#### Scenario: Audio pick reaches playback

- **WHEN** the viewer picks the second audio track on the detail page and presses Play
- **THEN** the player's first `playbackInfo` request carries that track's stream index as
  `selectedAudioIndex`
