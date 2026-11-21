## ADDED Requirements

### Requirement: The served web player can present styled subtitles

The served web player SHALL load its ASS/SSA renderer only when an ASS/SSA track is selected. It SHALL use the existing plain text presentation when styled rendering is unavailable.

#### Scenario: Selecting a styled muxed subtitle

- **WHEN** a viewer selects an ASS or SSA track
- **THEN** the player can retrieve its styled subtitle and the associated file's font attachments using the track's live playback grant
- **AND** non-font attachments cannot be retrieved

#### Scenario: Styled rendering fails

- **WHEN** the renderer cannot start or encounters an error
- **THEN** the player retains playback and shows the existing WebVTT track
