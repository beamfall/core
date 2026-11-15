# Hardware AV1 encode and Intel low-power fallback

Ticket: CORE-2094 (`queue:core:main`).

## Why

The hardware probe knew only H.264 and HEVC encoders, so hosts with AV1 encode hardware (Intel
Arc and newer iGPUs, NVIDIA Ada, recent AMD) could never encode AV1. Some Intel iGPUs expose only
the low-power (VDENC) encode path: their normal QSV/VA-API probe fails, so the accelerator was
reported unavailable and every transcode fell back to software.

## What Changes

- The probe knows `av1_qsv`, `av1_vaapi` and `av1_nvenc`. VideoToolbox has no AV1 encoder.
- AV1, like HEVC (CORE-2088), is probed only after the accelerator's H.264 probe passes. An AV1
  rendition whose encoder the probe did not verify takes the full software path. The runner
  reports AV1 as a transcode codec only when the encoder is verified.
- When the normal QSV or VA-API H.264 probe fails, the probe retries it with `-low_power 1`. If
  that passes, the accelerator is working in low-power mode: its HEVC and AV1 probes and every
  real encode on it pass `-low_power 1`. NVENC and VideoToolbox are never retried.
- The internal hardware-acceleration report and the startup log record AV1 and low-power mode per
  accelerator. Nothing is added to the wire.
- The probe chain is up to four encodes per candidate, so its default total timeout grows to 20s.

## Not in this change

Delivering AV1 to a client needs wire work, deferred to CORE-2100:

- a playback grant field for AV1 HLS (the analogue of the HEVC grant),
- AV1 ladder rungs,
- `av01` playlist `CODECS`.

Clients can already declare AV1 decode in their profile, but the server offers AV1 renditions only
once that work lands. The Console display of AV1 and low-power status is CORE-2100 too.

## Impact

- `internal/ffmpeg` (probe chain, encoder tables, encode argv), `internal/config` (report fields),
  `internal/app` (startup wiring and log).
