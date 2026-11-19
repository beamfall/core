## ADDED Requirements

### Requirement: Trickplay sprite sheets decode keyframes efficiently

Trickplay sprite-sheet generation SHALL use one decoder thread and first attempt keyframe-only decode. Verified VA-API/QSV
devices SHALL be used unless software-only policy is enabled; other accelerators SHALL use
software. File output SHALL retry failed hardware decode with software keyframes, then all
frames if keyframe decode fails. Pipe output SHALL use software keyframe decode without retries.
The `trickplay:v1` manifest and sprite-sheet layout SHALL remain unchanged.

#### Scenario: Hardware keyframe pass

- **WHEN** sprite sheets are generated with a verified VA-API or QSV device
- **THEN** FFmpeg decodes only keyframes using that device
- **AND** the resulting sheets retain the existing `trickplay:v1` layout

#### Scenario: Hardware decode fails

- **WHEN** the hardware sprite-sheet pass fails
- **THEN** generation retries with software keyframe decode

#### Scenario: Software-only generation

- **WHEN** software-only policy is enabled or the selected accelerator is neither VA-API nor QSV
- **THEN** sprite sheets use software keyframe decode with the same layout

#### Scenario: Software keyframe decode fails for file output

- **WHEN** the software keyframe pass fails while generating sprite-sheet files
- **THEN** generation retries decoding every frame in software, preserving the same layout

#### Scenario: Pipe output cannot be rewound

- **WHEN** the software keyframe pass fails while writing a sprite sheet to a pipe
- **THEN** generation returns the failure without retrying into the same pipe

### Requirement: Trickplay skips unsuitable or already generated sources

Trickplay SHALL succeed without generation when the fingerprint has a published sheet manifest,
no video source is available, duration is shorter than the default 10-second interval, or the
source is a disc image. Disc detection SHALL match `.iso`/`.img` extensions, complete
`BDMV`/`VIDEO_TS` path segments, and the `iso` container case-insensitively.

#### Scenario: Existing sheets

- **WHEN** the selected source fingerprint already has a trickplay manifest
- **THEN** generation returns successfully without decoding or rewriting sheets

#### Scenario: No video

- **WHEN** the Media has no eligible video source
- **THEN** trickplay returns successfully without generating assets

#### Scenario: Short video

- **WHEN** a video's duration is shorter than the default trickplay interval
- **THEN** trickplay returns successfully without generating assets

#### Scenario: Disc image

- **WHEN** the source has an ISO or IMG extension, an ISO container, or a BDMV or VIDEO_TS path segment
- **THEN** trickplay returns successfully without generating assets regardless of case

#### Scenario: Eligible boundary and similar path names

- **WHEN** a video's duration equals the interval and no disc-image rule matches
- **THEN** it remains eligible for generation
- **AND** substrings such as `not_BDMV` or `VIDEO_TS_extra` do not cause a skip
