# hardware-transcode Specification

## Purpose
How the server finds and uses hardware video encoders (Intel and AMD through QSV and VA-API, NVIDIA
through NVENC, Apple through VideoToolbox, AMD through AMF on Windows): which GPU it probes, which
codecs it verifies before advertising them, the rate control it encodes with, and how many realtime
transcodes it admits by default.

## Requirements

### Requirement: The server selects one render node for hardware transcode

On Linux the server SHALL enumerate the DRM render nodes at startup and read each node's PCI
vendor. It SHALL select a node from the stored transcode device when that device names an
enumerated render node path, or names a vendor (`intel`, `amd`, `nvidia`) that one of the nodes
has. Otherwise, or when the stored device matches no node, it SHALL select the first Intel node,
then the first AMD node, then the first node.

#### Scenario: Intel is preferred by default

- **WHEN** the host has an AMD node `renderD128` and an Intel node `renderD129` and no device is stored
- **THEN** the server selects `renderD129`

#### Scenario: A stored render node path is honoured

- **WHEN** the stored transcode device is `/dev/dri/renderD128` and that node exists
- **THEN** the server selects `renderD128`

#### Scenario: A stored vendor name is honoured

- **WHEN** the stored transcode device is `amd` and an AMD node exists
- **THEN** the server selects the first AMD node

#### Scenario: An unmatched hint falls back

- **WHEN** the stored transcode device is `/dev/dri/renderD200` and no such node exists
- **THEN** the server logs the mismatch and selects by the default order

### Requirement: Probe, tone-map and encode share the selected render node

The QSV and VA-API probe encodes, the QSV and VA-API tone-map devices and the QSV and VA-API
encodes SHALL all use the selected render node. The QSV probe and encode SHALL initialise the QSV
device on that node explicitly. The node list and the selection SHALL be reported in the server's
internal hardware report and startup log and SHALL NOT be added to the API.

#### Scenario: QSV encodes on the probed GPU

- **WHEN** the selected node is `/dev/dri/renderD129` and QSV is the verified encoder
- **THEN** both the QSV probe and the QSV encode argv carry `-init_hw_device qsv=bfqsv:hw,child_device=/dev/dri/renderD129`

### Requirement: The automatic transcode cap fits the host

When neither an environment variable or flag nor the Console sets a concurrent-transcode cap, the
server SHALL derive the cap from the host at startup. With no verified hardware encoder selected,
the cap SHALL be one session per four usable cores, at least 1 and at most 8. With a verified
hardware encoder selected, the cap SHALL be one session per usable core, at least 2, and at most
the selected backend's limit: 8 for NVENC, 6 for QSV and VA-API, and 4 for VideoToolbox.

#### Scenario: Software transcode on a small host

- **WHEN** no hardware encoder is verified and the host has 4 usable cores
- **THEN** the automatic cap is 1

#### Scenario: Software transcode on a large host

- **WHEN** no hardware encoder is verified and the host has 64 usable cores
- **THEN** the automatic cap is 8

#### Scenario: NVENC is bounded by the driver session limit

- **WHEN** NVENC is the selected encoder and the host has 16 usable cores
- **THEN** the automatic cap is 8

#### Scenario: Hardware transcode on a two-core host

- **WHEN** QSV is the selected encoder and the host has 2 usable cores
- **THEN** the automatic cap is 2

### Requirement: An operator cap overrides the automatic cap

An environment variable or flag cap SHALL take precedence over the stored Console cap, and the
stored Console cap SHALL take precedence over the automatic cap. The Console SHALL report the
automatic cap as the effective cap when no cap is stored.

#### Scenario: Stored cap wins over the automatic cap

- **WHEN** the automatic cap is 8 and the Console stores a cap of 3
- **THEN** the transcoder admits at most 3 sessions

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

### Requirement: Verified HDR sources decode, scale and tone-map on the device

The server SHALL decode, scale and tone-map an eligible HDR source on the selected render node and
hand device frames to the encoder, once the device-decode probe certified that chain for the
encoder backend and transfer class. A source is eligible when it is HEVC with a PQ or HLG picture,
the verified encoder is QSV or VA-API, and nothing in the rendition needs a system-memory filter.
Otherwise the server SHALL emit the arguments it emitted without device decode.

#### Scenario: A verified source stays on the device

- **WHEN** the encoder is QSV, the source is HEVC PQ at rendition height 1080 and the device-decode probe passed
- **THEN** the argv carries `-hwaccel vaapi -hwaccel_output_format vaapi` before `-i`, its filter graph is `scale_vaapi`, `hwmap` to OpenCL, `tonemap_opencl`, `hwmap` back to QSV, and it has no `hwupload`, `hwdownload` or software `scale`

#### Scenario: A Dolby Vision profile 8 source uses its base layer

- **WHEN** the source is HEVC Dolby Vision profile 8 with a PQ or HLG base layer and the device-decode probe passed for that class
- **THEN** the argv equals the one produced for a plain HEVC PQ source

#### Scenario: An unverified host keeps the software path

- **WHEN** the device-decode probe failed for the encoder backend
- **THEN** the argv equals the one produced without device decode

#### Scenario: An ineligible source keeps its arguments

- **WHEN** the source is H.264, or carries Dolby Vision profile 5 or 7, or the rendition burns in a subtitle
- **THEN** the argv equals the one produced without device decode

### Requirement: The device-decode probe certifies the shipped chain against a decoded control

The device-decode probe SHALL decode an HEVC Main10 sample with the same device initialisation,
input options and filter legs the transcode emits, and SHALL certify the chain only when the
tone-mapped highlight drops below a decoded pass-through control by at least the ADR-0075 floor,
stays above the crush floor, and the control itself does not pass against a software decode of
the same sample. A sample that cannot be produced or any leg that fails SHALL leave the chain
unverified.

#### Scenario: The chain changes the picture

- **WHEN** the control highlight is 176 and the candidate highlight is 116
- **THEN** the probe certifies the chain

#### Scenario: The tone-map leg does nothing

- **WHEN** the candidate highlight equals the control highlight
- **THEN** the probe reports no transform and the chain is not used

### Requirement: The hardware tone-map control leg is runnable

The pass-through control of an upload tone-map chain SHALL download frames in the format they
were uploaded in.

#### Scenario: The VA-API control leg

- **WHEN** the VA-API upload chain's control is built
- **THEN** its filters are `format=p010`, `hwupload`, `hwdownload`, `format=p010`

### Requirement: The server image can run OpenCL on Intel GPUs

The server image SHALL include the Intel OpenCL driver so an OpenCL device can be derived from the
VA-API device.

#### Scenario: The image is built

- **WHEN** the runtime image is built
- **THEN** `intel-opencl-icd` is installed
