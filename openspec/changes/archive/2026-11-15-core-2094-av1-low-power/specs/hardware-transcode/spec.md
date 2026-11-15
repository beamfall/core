## ADDED Requirements

### Requirement: AV1 hardware encode is verified before use

The hardware probe SHALL probe an accelerator's AV1 encoder (`av1_qsv`, `av1_vaapi` or
`av1_nvenc`) only after that accelerator's H.264 probe passes. The server SHALL encode AV1 on an
accelerator only when its AV1 probe passed. An AV1 rendition whose encoder was not verified SHALL
take the software path as a whole.

#### Scenario: AV1 is verified behind H.264

- **WHEN** the QSV H.264 probe and the `av1_qsv` probe pass
- **THEN** QSV is reported AV1-capable and the runner reports AV1 as a transcode codec

#### Scenario: Unverified AV1 takes the software path

- **WHEN** the selected accelerator's AV1 probe failed and an AV1 rendition is encoded
- **THEN** the rendition uses no hardware upload filters and no hardware encoder

#### Scenario: No AV1 probe behind a failed H.264 probe

- **WHEN** an accelerator's H.264 probe fails
- **THEN** its AV1 encoder is not probed

### Requirement: Intel accelerators fall back to low-power encode

When the normal QSV or VA-API H.264 probe fails, the probe SHALL retry it with `-low_power 1`. If
the retry passes, the accelerator SHALL be reported working in low-power mode, and its HEVC and
AV1 probes and its real encodes SHALL pass `-low_power 1`. NVENC and VideoToolbox SHALL NOT be
retried, and SHALL NOT be given `-low_power`. Low-power mode SHALL be reported in the internal
hardware report and startup log and SHALL NOT be added to the API.

#### Scenario: Normal encode passes

- **WHEN** the normal VA-API H.264 probe passes
- **THEN** no low-power retry runs and VA-API encodes without `-low_power`

#### Scenario: Low-power encode passes after the normal one fails

- **WHEN** the normal VA-API H.264 probe fails and the `-low_power 1` retry passes
- **THEN** VA-API is working in low-power mode and its encode argv carries `-c:v h264_vaapi -low_power 1`

#### Scenario: Both encodes fail

- **WHEN** the normal and the low-power QSV H.264 probes both fail
- **THEN** QSV is not working and none of its other encoders is probed
