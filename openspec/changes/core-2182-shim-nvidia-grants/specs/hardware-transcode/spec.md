## ADDED Requirements

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
