## ADDED Requirements

### Requirement: Caption tracks load on first show

The web player SHALL request a caption track's WebVTT only when that track is first shown. The
server SHALL finish a muxed-subtitle conversion it has started, and cache the result, when the
request that started it is cancelled.

#### Scenario: Playback starts without caption requests

- **WHEN** playback starts for a file with five caption tracks and none selected
- **THEN** the player adds five caption tracks and requests none of their WebVTT URLs

#### Scenario: Selecting a caption loads only that track

- **WHEN** the viewer selects one caption track
- **THEN** the player requests that track's WebVTT URL and no other

#### Scenario: A cancelled request does not discard a conversion

- **WHEN** a request starts a muxed-subtitle conversion and is cancelled before it finishes
- **THEN** the conversion completes and the next request for that stream is served from the cache

#### Scenario: A request cancelled while waiting starts no conversion

- **WHEN** a request is cancelled while it waits for another conversion to finish
- **THEN** it fails without starting a conversion
