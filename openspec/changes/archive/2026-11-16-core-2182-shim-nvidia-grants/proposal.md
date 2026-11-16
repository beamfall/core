# Confined NVENC and CUDA jobs and probes: NVIDIA device grants

Ticket: CORE-2182 (`queue:core:main`).

## Why

CORE-2158 gave confined QSV and VA-API jobs, and their probes, the DRM render nodes they need, but
an NVENC encode or CUDA tone-map still runs under a landlock ruleset with no NVIDIA device node.
libcuda cannot open `/dev/nvidiactl`, so the NVENC probe, which now runs through the same shim,
reports the encoder unavailable and the host falls back to software encoding on a machine with a
working NVIDIA GPU.

## What Changes

- The FFmpeg hardening shim accepts a `--nvidia` flag. With it, the Linux landlock ruleset adds,
  each only when the path exists on the host:
  - read, write and (on landlock ABI 5+) device ioctl on `/dev/nvidiactl` (the driver control
    node), `/dev/nvidia-uvm` (CUDA unified memory) and each per-GPU node `/dev/nvidia<N>`, granted
    file by file so `/dev` is never listable;
  - read-only access to `/proc/driver/nvidia`, where libcuda reads the driver version and
    parameters and enumerates the GPUs;
  - read-only access to each GPU's own sysfs PCI device directory, resolved from the bus IDs under
    `/proc/driver/nvidia/gpus` and kept only when it lies beneath `/sys/devices`.
- Never granted: `/dev/nvidia-modeset` (display only), `/dev/nvidia-uvm-tools` (profiler events),
  the `/dev/nvidia-caps` MIG nodes, a listing of `/dev`, and any other part of `/proc` or `/sys`.
- No library path is added. libcuda, libnvidia-encode and the PTX JIT compiler install under the
  library trees the shim already grants (`/usr/lib`, `/lib`, `/usr/lib64`, `/lib64`,
  `/usr/local/lib`), and the loader cache and `ld.so.conf.d` are already readable.
- An HLS transcode asks for `--nvidia` only when its encode or tone-map runs on NVENC/CUDA; every
  other job keeps its existing ruleset. `--dri` and `--nvidia` are independent.
- The encoder and tone-map probes pass `--nvidia` exactly when the transcode they qualify would, so
  a probe's verdict reflects the confinement the job will run under.

## Not in this change

- **The hardware run is outstanding.** No NVIDIA host with landlock ABI 3+ has run a confined
  NVENC encode or CUDA tone-map yet. That run should confirm that `nvidia-uvm-tools` and no further
  `/proc` or `/sys` path is needed, and record any landlock denials it hits.
- `/dev/nvidia-uvm` may be created lazily by the setuid `nvidia-modprobe` on first CUDA use. A
  confined job runs with no_new_privs and cannot do that, so the node must already exist (loaded
  by the host's driver setup or an earlier unconfined CUDA user).
- Driver libraries outside the granted trees, such as NixOS's `/run/opengl-driver/lib` or a
  driver under `/opt`, are not granted.
- The CUDA JIT cache (`~/.nv/ComputeCache`) is not writable; kernels are compiled in memory per job.

## Impact

- `cmd/beamfall`: shim flag parsing and the landlock ruleset.
- `internal/ffmpeg`: shim argv, HLS confinement selection and probe confinement.
