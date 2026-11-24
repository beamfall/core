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

### Requirement: The served web player can present styled subtitles

The served web player SHALL load its ASS/SSA renderer only when an ASS/SSA track is selected. It SHALL use the existing plain text presentation when styled rendering is unavailable.

#### Scenario: Selecting a styled muxed subtitle

- **WHEN** a viewer selects an ASS or SSA track
- **THEN** the player can retrieve its styled subtitle and the associated file's font attachments using the track's live playback grant
- **AND** non-font attachments cannot be retrieved

#### Scenario: Styled rendering fails

- **WHEN** the renderer cannot start or encounters an error
- **THEN** the player retains playback and shows the existing WebVTT track

### Requirement: Styled subtitles are burned in for clients that cannot render them

When the server's FFmpeg provides the libass `ass` filter, the server SHALL burn a selected ASS or
SSA stream into the HLS picture for a Capability Profile that does not declare `ass`, rendering it
with libass from the extracted script and the file's font attachments. A Capability Profile that
declares `ass` SHALL keep receiving the styled sidecar. When it cannot burn, the server SHALL keep
delivering the stream as a WebVTT sidecar.

#### Scenario: Positioned anime subtitles on a client without an ASS renderer

- **WHEN** a client whose profile omits `ass` plays a file with an ASS track selected on a server
  whose FFmpeg has the `ass` filter
- **THEN** the decision is a transcode whose grant burns that stream
- **AND** the session renders the script with libass using the file's embedded fonts

#### Scenario: Seeking keeps burned subtitles in sync

- **WHEN** a burn-in session restarts its encode at a later segment
- **THEN** the subtitle events shown match the source time of that segment

#### Scenario: A client that renders ASS

- **WHEN** a client whose profile declares `ass` selects the same track
- **THEN** the decision does not burn the stream in

#### Scenario: No libass on the server

- **WHEN** the server's FFmpeg lacks the `ass` filter
- **THEN** the ASS track is delivered as a WebVTT sidecar, as before

#### Scenario: Other cases that cannot burn

- **WHEN** the Library is encrypted, the media is audio, the profile may not transcode, or no full
  transcode can re-encode the source (such as Dolby Vision profile 5)
- **THEN** the ASS track is delivered as a WebVTT sidecar

### Requirement: A caption track the server burns in has no WebVTT URL

A muxed caption track that the server would burn in for the requesting Capability Profile SHALL be
advertised with its stream index and without a WebVTT URL, so a native client routes it to burn-in
as it does a bitmap track. A profile that declares `webvtt` SHALL keep the URL.

#### Scenario: Native clients see the track as burn-in only

- **WHEN** the server would burn an ASS track in for the requesting profile
- **THEN** that track's caption entry carries its stream index and no WebVTT URL
- **AND** a plain text track in the same file keeps its WebVTT URL

#### Scenario: A sidecar consumer keeps the flattened track

- **WHEN** a profile that declares `webvtt` but not `ass`, such as a Cast receiver, requests playback
- **THEN** the ASS track keeps its WebVTT URL
