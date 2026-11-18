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

### Requirement: Confined hardware jobs get only the render nodes and the OpenCL registry

When the FFmpeg hardening shim confines a job that encodes or tone-maps on QSV or VA-API, the
Linux landlock ruleset SHALL add, each only when the path exists: directory listing of
`/dev/dri`, read, write and device-ioctl access to each `/dev/dri/renderD*` render node, and
read-only access to `/etc/OpenCL/vendors`. It SHALL NOT grant DRM primary (`card*`) nodes, and a
job without a QSV or VA-API encode or tone-map SHALL NOT receive any of these grants.

#### Scenario: Hardware rendition

- **WHEN** an HLS transcode runs with a VA-API encoder or a VA-API OpenCL tone-map
- **THEN** its shim invocation carries `--dri` and its ruleset grants the render nodes and `/etc/OpenCL/vendors` read-only

#### Scenario: Software rendition

- **WHEN** an HLS transcode runs on the software encoder and tone-map
- **THEN** its shim invocation carries no `--dri` and its ruleset grants nothing under `/dev/dri` or `/etc/OpenCL`

#### Scenario: Primary nodes stay closed

- **WHEN** the ruleset grants the render nodes
- **THEN** no `/dev/dri/card*` node is granted

### Requirement: Each granted render node's sysfs device is readable

With the render-node grants, the ruleset SHALL also grant read-only access to each render node's
own sysfs device directory, the resolved `/sys/dev/char/MAJOR:MINOR/device`, where libdrm reads
the PCI IDs the drivers need. It SHALL NOT grant any other part of `/sys`.

#### Scenario: Driver discovery reads the render node's device

- **WHEN** the ruleset grants a render node whose sysfs device directory resolves
- **THEN** that directory is granted read-only and no other sysfs path is granted

### Requirement: Hardware probes run under the transcode's confinement

The encoder, tone-map and device-decode probes SHALL run FFmpeg through the same hardening shim
as the transcode they certify, with the same device grants: `--dri` for a QSV or VA-API probe and
none for any other accelerator. The device-decode probe SHALL write its sample to a private
directory and grant only that directory read-only. When no shim is configured, probes and
transcodes SHALL both exec FFmpeg directly.

#### Scenario: Tone-map probe on VA-API

- **WHEN** the server probes the VA-API OpenCL tone-map chain with the shim configured
- **THEN** every render runs as `ffmpeg-shim --dri -- ffmpeg …`

#### Scenario: Device-decode probe

- **WHEN** the server probes a device-decode chain with the shim configured
- **THEN** each leg runs through the shim with the sample's private directory as its only read-only job path

#### Scenario: A grant the device needs is missing

- **WHEN** the confined probe cannot open the device or load the OpenCL ICD
- **THEN** the probe reports the chain unavailable and the transcode stays on software

### Requirement: Confined NVENC and CUDA jobs get only the NVIDIA nodes and driver paths

When the FFmpeg hardening shim confines a job that encodes or tone-maps on NVENC/CUDA, the Linux
landlock ruleset SHALL add, each only when the path exists: read, write and device-ioctl access to
`/dev/nvidiactl`, `/dev/nvidia-uvm` and each `/dev/nvidia<N>` GPU node; read-only access to
`/proc/driver/nvidia`; and read-only access to each NVIDIA GPU's sysfs PCI device directory. A job
without an NVENC/CUDA encode or tone-map SHALL NOT receive any of these grants.

#### Scenario: NVENC rendition

- **WHEN** an HLS transcode runs with the NVENC encoder or a CUDA tone-map
- **THEN** its shim invocation carries `--nvidia` and its ruleset grants the present NVIDIA nodes, `/proc/driver/nvidia` read-only and each GPU's sysfs device directory read-only

#### Scenario: Non-NVIDIA rendition

- **WHEN** an HLS transcode runs on the software, QSV or VA-API encoder and tone-map
- **THEN** its shim invocation carries no `--nvidia` and its ruleset grants nothing under `/dev/nvidia*`, `/proc/driver/nvidia` or an NVIDIA GPU's sysfs directory

### Requirement: The NVIDIA grant excludes display, tools and MIG nodes

The NVIDIA grant SHALL NOT include `/dev/nvidia-modeset`, `/dev/nvidia-uvm-tools`,
`/dev/nvidia-caps`, a listing of `/dev`, or any other part of `/proc` or `/sys`.

#### Scenario: Display and tools nodes stay closed

- **WHEN** the ruleset grants the NVIDIA nodes
- **THEN** `/dev/nvidia-modeset`, `/dev/nvidia-uvm-tools` and `/dev/nvidia-caps` are not granted

### Requirement: NVENC and CUDA probes run under the transcode's NVIDIA grant

The encoder and tone-map probes SHALL run through the hardening shim with `--nvidia` exactly when
the probed encoder or tone-map backend is NVENC/CUDA, the same grant the transcode gets.

#### Scenario: NVENC encoder probe

- **WHEN** the server probes `h264_nvenc` with the shim configured
- **THEN** the probe runs as `ffmpeg-shim --nvidia -- ffmpeg …` and carries no `--dri`

#### Scenario: CUDA tone-map probe

- **WHEN** the server probes the CUDA tone-map chain with the shim configured
- **THEN** every render runs with `--nvidia`

### Requirement: The startup log names the effective transcode cap

At startup the server SHALL log the transcode cap in force, its source and the automatic cap. The
source SHALL be `env/flag (BEAMFALL_TRANSCODE_CAP)`, `console` or `automatic`, in the precedence
of "An operator cap overrides the automatic cap".

#### Scenario: Env cap below the automatic cap

- **WHEN** the automatic cap is 6 and `BEAMFALL_TRANSCODE_CAP` is 2
- **THEN** the log reports cap 2 from `env/flag (BEAMFALL_TRANSCODE_CAP)`, with automatic cap 6

### Requirement: The Console hardware switch decides the encoder of new sessions

While the Console hardware-acceleration switch is off, every new transcode session SHALL encode its video with
`libx264` and SHALL NOT use a hardware encoder, device decode or a hardware tone-map, and the server
SHALL NOT offer HEVC transcode output. While it is on, new sessions SHALL use the encoder the
startup probe verified. A change SHALL apply to sessions started after the save, without a restart.

#### Scenario: Hardware off forces software encoding

- **WHEN** NVENC is the verified encoder and the operator turns hardware acceleration off
- **THEN** the next transcode session's FFmpeg argv carries `-c:v libx264` and no `h264_nvenc`

#### Scenario: Hardware off stops HEVC output

- **WHEN** the verified encoder supports HEVC and hardware acceleration is off
- **THEN** the server does not offer HEVC transcode output

### Requirement: The hardware switch defaults to on

The server SHALL store the hardware-acceleration switch under the `transcode.hardware` key, and the
switch SHALL read on when that key is absent. The earlier `transcode.hardware_enabled` key SHALL be
ignored.

#### Scenario: An upgraded server keeps hardware

- **WHEN** the store holds `transcode.hardware_enabled = 0` and no `transcode.hardware` key
- **THEN** the switch reads on and sessions use the verified hardware encoder

### Requirement: The automatic cap follows the hardware switch

When neither an environment variable or flag nor the Console sets a concurrent-transcode cap and
the hardware switch is off, the automatic cap SHALL be the software cap for the host. A change of
the switch SHALL apply the matching automatic cap live.

#### Scenario: Turning hardware off lowers the automatic cap

- **WHEN** QSV is selected on an 8-core host with the automatic cap and the operator turns hardware off
- **THEN** the cap in force becomes 2

### Requirement: The Console codec setting limits transcode output

The codec setting SHALL accept Automatic (empty) and `h264`. Automatic SHALL offer HEVC transcode
output when the device and the verified hardware encoder support it, and H.264 otherwise. `h264`
SHALL make new sessions H.264 only. A write of `hevc` SHALL be accepted and stored as Automatic.

#### Scenario: H.264 only

- **WHEN** the codec setting is `h264` and the device and verified encoder support HEVC
- **THEN** the server offers only H.264 transcode output

#### Scenario: Legacy HEVC value

- **WHEN** a client writes codec `hevc`
- **THEN** the stored codec reads back as Automatic

### Requirement: The Console quality setting tunes the software encoder

The quality setting SHALL accept Automatic (empty), `low`, `balanced` and `high`, and Automatic
SHALL mean `balanced`. For `libx264`, `low` SHALL use CRF 28, `balanced` CRF 23 and `high` CRF 20.
A change SHALL apply to sessions started after the save.

#### Scenario: High quality in software

- **WHEN** the quality setting is `high` and the session encodes in software
- **THEN** the FFmpeg argv carries `-crf 20`

### Requirement: The Console quality setting tunes hardware encoders

`balanced` SHALL keep the default hardware arguments. For a hardware encoder with a rung bitrate, `low` SHALL set `-b:v` to three
quarters of the rung while `-maxrate` stays at the rung. `high` SHALL select NVENC preset `p6`, QSV
preset `medium` and AMF `-quality balanced`; on VA-API and VideoToolbox it SHALL keep the balanced
arguments.

#### Scenario: Low quality on NVENC

- **WHEN** the quality setting is `low` and a 4000 kb/s rung encodes on NVENC
- **THEN** the FFmpeg argv carries `-b:v 3000k` and `-maxrate 4000k`
