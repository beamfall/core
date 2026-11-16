## ADDED Requirements

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
