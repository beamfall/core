# muxed-subtitle-extraction Specification

## Purpose
Serve subtitle tracks muxed into a media file quickly and predictably: extract each file's text tracks once, never make one file's request wait on another, and answer a slow request with a bounded retry instead of a hang.

## Requirements

### Requirement: Muxed subtitle extraction does not serialize unrelated files

The muxed-subtitle byte route SHALL run at most one extraction per source file, shared by every
request for that file's tracks, and SHALL NOT make a request for one file wait on an extraction of
another file.

#### Scenario: Two requests for the same track

- **WHEN** two requests for the same uncached muxed subtitle arrive together
- **THEN** ffmpeg runs once and both requests are served from its result

#### Scenario: A slow file does not block another

- **WHEN** an extraction of one file is still running and a subtitle of another file is requested
- **THEN** the other file's subtitle is extracted and served without waiting

### Requirement: Every text track of a file is extracted in one pass

The first extraction for a file SHALL convert every text subtitle stream of that file that is not
already cached in a single ffmpeg run, and SHALL NOT include bitmap subtitle streams.

#### Scenario: Switching subtitle tracks

- **WHEN** a viewer opens one text track of a file with two text tracks and then switches to the other
- **THEN** ffmpeg ran one pass covering both tracks and the second request is a cache hit

### Requirement: A subtitle request waits a bounded time

A subtitle request SHALL wait at most a bounded time for an extraction. When the bound runs out it
SHALL answer `503 Service Unavailable` with a `Retry-After` header, and the extraction SHALL keep
running so that a retry is served from the cache.

#### Scenario: Long extraction

- **WHEN** an extraction takes longer than the wait bound
- **THEN** the request gets `503` with `Retry-After: 5`, and a later retry gets the WebVTT without
  a second extraction
