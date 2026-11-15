# Host-sized default transcode cap

Ticket: CORE-2093 (`queue:core:main`).

## Why

With no cap stored, the server admitted one software transcode or four hardware transcodes whatever
the host. A 32-core box ran one libx264 session; an NVENC card that the consumer driver allows eight
sessions on, or an Intel iGPU with no session limit, got the same four as a Mac. The default should
fit the host it runs on.

## What Changes

- The automatic (probe-derived) cap now derives from the host:
  - Software transcode: one session per four usable cores, clamped to 1..8. Usable cores are
    `runtime.GOMAXPROCS(0)`, which honours CPU affinity and cgroup CPU limits.
  - Hardware transcode: decode, scaling, audio and muxing still run on the CPU, so the cap is one
    session per usable core, at least 2, bounded by the backend: NVENC 8 (the consumer driver's
    concurrent-session limit on current drivers), QSV and VA-API 6 (integrated GPUs have no
    session limit; six realtime 1080p encodes is a conservative throughput figure), VideoToolbox 4
    (one media engine on base Apple silicon).
  - The cap follows the accelerator the server selected, so a verified configured preference
    decides the backend limit.
- The operator override is unchanged: an environment variable or flag cap wins, then the stored
  Console cap, then this automatic value. The Console already reports the automatic value as
  `effectiveMaxConcurrentTranscodes` when nothing is stored.
- The startup log names the derived cap and the accelerator it came from.

## Impact

- `internal/ffmpeg` (cap derivation), `internal/app` (startup wiring and log).
- No wire change: `transcodeConfig` keeps its fields; only the automatic value differs.
