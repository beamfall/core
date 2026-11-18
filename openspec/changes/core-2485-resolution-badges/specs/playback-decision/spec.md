## ADDED Requirements

### Requirement: Resolution labels

Core SHALL label a known video frame size with exactly one of `8K`, `4K`, `1080p`, `720p` or `SD`,
choosing the highest class that either the width or the height reaches, and SHALL give no label for
an unknown frame size. `playbackInfo` SHALL report the negotiated file's source video (resolution
label, width, height and HDR format) and SHALL label each advertised rendition by its height and
the source aspect. An audio-class item SHALL carry no source video and no rendition labels.

#### Scenario: Scope 4K source transcoded to 1080p

- **WHEN** a 3840x1600 HDR10 file is transcoded with 1080p and 720p renditions
- **THEN** `sourceVideo` reports `4K` and `hdr10`, and the renditions report `1080p` and `720p`

#### Scenario: Version with an unknown frame size

- **WHEN** the negotiated Version is not the file the Media row was probed from
- **THEN** `sourceVideo` carries its HDR format and no resolution, width or height

#### Scenario: Audio with cover art

- **WHEN** an audio-class item's file carries cover art as a video stream
- **THEN** `playbackInfo` reports no `sourceVideo` and no rendition resolution
