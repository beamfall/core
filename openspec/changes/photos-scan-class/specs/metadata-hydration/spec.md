## ADDED Requirements

### Requirement: Local-file extractors receive the class the file actually has

The scanner SHALL offer a file to plugin local-metadata extractors with class `image` for a still-image extension and `video` for a file whose probe found a video stream, and SHALL NOT offer audio-only or unprobed non-image files. A file that produced local metadata SHALL import standalone, not as a queued Part or attached Version, so its extracted fields are applied. An unchanged file on the unprobed fast path is not re-extracted until it changes.

#### Scenario: A camera-roll clip gets its capture date

- **WHEN** a scan finds a `.mov` clip with a video stream and a plugin extractor that reads its capture date
- **THEN** the extractor is called with class `video` and the clip's Media carries the extracted capture date

#### Scenario: An audio file is not offered to extractors

- **WHEN** a scan finds an audio-only file
- **THEN** no local-metadata extractor is called for it
