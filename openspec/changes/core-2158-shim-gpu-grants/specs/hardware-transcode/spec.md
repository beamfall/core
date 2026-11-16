## ADDED Requirements

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
