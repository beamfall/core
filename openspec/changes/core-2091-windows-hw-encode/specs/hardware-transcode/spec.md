## ADDED Requirements

### Requirement: Windows hosts probe NVENC, QSV and AMF

On Windows the hardware probe SHALL try NVENC, QSV and AMF, each verified by an H.264 probe
before its HEVC and AV1 encoders are probed. The `amf` accelerator SHALL be a probe candidate and
a hardware-acceleration report row only on Windows hosts.

#### Scenario: Windows candidates

- **WHEN** the probe runs on a Windows host
- **THEN** it tries `h264_nvenc`, `h264_qsv` and `h264_amf`, and never VA-API or VideoToolbox

#### Scenario: AMF is absent from other platforms' reports

- **WHEN** the probe runs on a Linux or macOS host
- **THEN** the report has no `amf` row

### Requirement: Windows QSV runs on D3D11

On Windows, QSV probes and encodes SHALL initialise the QSV device with
`child_device_type=d3d11va` and SHALL NOT name a DRM render node.

#### Scenario: Windows QSV init

- **WHEN** a QSV encode runs on a Windows host
- **THEN** its argv carries `-init_hw_device qsv=bfqsv:hw_any,child_device_type=d3d11va`

### Requirement: AMF encodes use capped VBR

An AMF encode with a bitrate target SHALL pass `-quality speed -rc vbr_peak -b:v <target>`
followed by `-maxrate <target> -bufsize <2×target>`, and SHALL hand the encoder NV12 frames.

#### Scenario: AMF rate control

- **WHEN** an H.264 rendition with a 6000 kbps target encodes on AMF
- **THEN** its argv carries `-c:v h264_amf -quality speed -rc vbr_peak -b:v 6000k -maxrate 6000k -bufsize 12000k`
