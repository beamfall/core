## ADDED Requirements

### Requirement: Track pickers before playback

The web Media detail page SHALL show a Version picker when the item has two or more Versions, an
Audio picker when the shown Version has two or more audio tracks, and a Subtitles picker with an
Off choice when it has caption tracks. A Version pick SHALL be remembered for the profile the way the
player remembers it. The pickers SHALL be hidden when the listing fails.

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

### Requirement: Automatic Version choice

The Version picker SHALL offer Auto, the server's choice, and SHALL show it until a Version is
picked or remembered. An audio or subtitle pick SHALL pick the Version whose tracks are shown.

#### Scenario: No Version picked

- **WHEN** the item has two Versions and none is picked or remembered
- **THEN** the Version picker shows Auto and Play leaves the Version to the server

### Requirement: Picked Version rides the navigation state

Starting playback from the Media detail page SHALL carry the picked Version in the watch route's
navigation state, and the player SHALL ask `playbackInfo` for that Version whether or not it could
be remembered.

#### Scenario: Version pick without storage

- **WHEN** browser storage is blocked and the viewer picks the second Version and presses Play
- **THEN** the player's first `playbackInfo` request asks for that Version

### Requirement: Picks apply on every start

Every playback start from the Media detail page SHALL apply the picks it carries, also when the
player is already playing that item. A pick made in the player afterwards SHALL stand until the
next start that carries picks.

#### Scenario: Start over while docked

- **WHEN** the item is playing docked, the viewer picks another audio track on its detail page and
  presses Start over
- **THEN** the restarted stream asks for the new audio track
