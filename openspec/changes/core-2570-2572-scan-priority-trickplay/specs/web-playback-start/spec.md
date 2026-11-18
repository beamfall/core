## ADDED Requirements

### Requirement: Trickplay sprite sheets decode keyframes efficiently

Trickplay sprite-sheet generation SHALL first attempt keyframe-only decode. Verified VA-API/QSV
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
