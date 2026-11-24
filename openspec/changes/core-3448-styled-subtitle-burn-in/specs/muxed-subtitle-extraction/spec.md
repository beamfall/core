## ADDED Requirements

### Requirement: Styled subtitles are burned in for clients that cannot render them

When the server's FFmpeg provides the libass `ass` filter, the server SHALL burn a selected ASS or
SSA stream into the HLS picture for a Capability Profile that does not declare `ass`, rendering it
with libass from the extracted script and the file's font attachments. A Capability Profile that
declares `ass` SHALL keep receiving the styled sidecar. Without the `ass` filter, or on an
Encrypted Library, the server SHALL keep delivering the stream as a WebVTT sidecar.

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

### Requirement: A caption track the server burns in has no WebVTT URL

A muxed caption track that the server would burn in for the requesting Capability Profile SHALL be
advertised with its stream index and without a WebVTT URL, so a native client routes it to burn-in
as it does a bitmap track.

#### Scenario: Native clients see the track as burn-in only

- **WHEN** the server would burn an ASS track in for the requesting profile
- **THEN** that track's caption entry carries its stream index and no WebVTT URL
- **AND** a plain text track in the same file keeps its WebVTT URL
