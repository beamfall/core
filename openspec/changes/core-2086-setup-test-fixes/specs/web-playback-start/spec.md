## ADDED Requirements

### Requirement: Caption tracks load on first show

The web player SHALL request a caption track's WebVTT only when that track is first shown.

#### Scenario: Playback starts without caption requests

- **WHEN** playback starts for a file with five caption tracks and none selected
- **THEN** the player adds five caption tracks and requests none of their WebVTT URLs

#### Scenario: Selecting a caption loads only that track

- **WHEN** the viewer selects one caption track
- **THEN** the player requests that track's WebVTT URL and no other
