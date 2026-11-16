# Confined hardware transcodes and probes: render-node and OpenCL grants

Ticket: CORE-2158 (`queue:core:main`).

## Why

The `ffmpeg-shim` landlock ruleset grants no `/dev/dri` render node and no `/etc/OpenCL/vendors`
ICD registry, while the encoder, tone-map and device-decode probes run FFmpeg directly, outside
the shim. On a landlock host with an Intel GPU a probe can report a hardware chain working and the
confined transcode then fail to open the render node or load the OpenCL ICD, with no software
retry. The probe does not predict the job it certifies.

## What Changes

- The shim gains a `--dri` flag. With it, and only on Linux, the ruleset adds, each only when
  present: `/dev/dri` directory listing (no file access), read, write and (landlock ABI 5+)
  device-ioctl on each `/dev/dri/renderD*` render node, read-only on each render node's own
  sysfs PCI device directory (libdrm's `drmGetDevice2` reads the vendor and device IDs there and
  the iHD and Mesa drivers fail without them), and read-only `/etc/OpenCL/vendors`. Primary
  (`card*`) nodes, the rest of `/sys` and vendor device nodes are never granted.
- An HLS transcode asks for `--dri` only when its rendition encodes or tone-maps on QSV or VA-API.
  Software, audio-only, composed, pipe and encrypted-live jobs keep the unchanged ruleset.
- The encoder, tone-map and device-decode probes run through the same shim with the same grants
  their transcode would get: `--dri` for a QSV or VA-API probe, and the device-decode sample in a
  private directory granted read-only. Without a configured shim (tests, non-server processes)
  they exec FFmpeg directly, as transcodes do.
- `beamfall serve` configures the probe shim alongside the transcode shim.

## Not in this change

- A run on a Linux host with landlock ABI 3+ and an Intel GPU. None is available; the acceptance
  receipt stays open on CORE-2158.
- NVIDIA (`/dev/nvidia*`) grants. An NVENC probe now runs confined, so on a landlock host it
  reports unavailable and the host transcodes in software instead of failing the job.

## Impact

- `cmd/beamfall` (shim flag parsing, landlock ruleset).
- `internal/ffmpeg` (shim argv, HLS confinement, probe execution).
- Windows: the probes now go through the shim, which fails closed there as transcodes already do.
