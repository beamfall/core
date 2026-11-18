## ADDED Requirements

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
