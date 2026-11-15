# web-playback-start Specification

## Purpose
Start web playback as soon as the stream is ready: optional data never delays it, resumed sessions begin at the resume position, and player code is prefetched on intent.

## Requirements

### Requirement: Optional data never delays the stream

The web player SHALL attach the stream as soon as playback info resolves, and SHALL NOT wait for
markers or the trickplay manifest before doing so.

#### Scenario: Markers request hangs

- **WHEN** playback info resolves and the markers request has not resolved
- **THEN** the player attaches the stream and enters the playing state

#### Scenario: Markers arrive late

- **WHEN** markers or trickplay resolve while the player is still playing
- **THEN** the player applies them to its state

#### Scenario: Markers arrive after playback ended

- **WHEN** markers or trickplay resolve after the player left the playing state
- **THEN** the player discards them

### Requirement: Resumed hls.js sessions start at the resume position

The web player SHALL pass the resume position to hls.js as its start position, and SHALL NOT seek
on metadata for an hls.js session.

#### Scenario: Resume through hls.js

- **WHEN** a session with a resume position plays through hls.js
- **THEN** hls.js is created with that start position and the player performs no metadata seek

#### Scenario: Resume through native playback

- **WHEN** a session with a resume position plays through the native video source
- **THEN** the player seeks to the resume position on metadata

#### Scenario: Stream-token refresh

- **WHEN** the player reloads the hls.js source after a stream-token refresh
- **THEN** loading restarts at the current playback position

### Requirement: Player code is prefetched on intent

The viewer SHALL begin loading the player route chunk when a media card receives pointer entry or
focus.

#### Scenario: Pointer enters a media card

- **WHEN** a pointer enters or focus lands on a media card
- **THEN** the player route chunk load starts, once, without changing visible state

#### Scenario: Non-media card

- **WHEN** a pointer enters a card that does not play media
- **THEN** no player chunk load starts

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
