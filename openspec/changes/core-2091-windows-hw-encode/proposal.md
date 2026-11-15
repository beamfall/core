# Windows hardware encode: NVENC, QSV and AMF

Ticket: CORE-2091 (`queue:core:main`). Depends on CORE-1042 (core builds for Windows).

## Why

The hardware probe chose no candidate on Windows, so a Windows host always transcoded in
software, even with an NVIDIA, Intel or AMD GPU. AMD's encoder on Windows is AMF, which the
accelerator vocabulary did not know.

## What Changes

- The accelerator vocabulary gains `amf` (AMD Advanced Media Framework: `h264_amf`, `hevc_amf`,
  `av1_amf`). It is a Windows-only candidate; on Linux, AMD GPUs keep encoding through VA-API.
- On Windows the probe tries NVENC, QSV and AMF, with the same H.264-first chain as Linux
  (CORE-2088, CORE-2094). Low-power retry stays QSV/VA-API only.
- QSV on Windows initialises its device on D3D11 (`-init_hw_device qsv=<name>:hw_any,child_device_type=d3d11va`)
  instead of a DRM render node.
- AMF encodes take software frames as NV12 and get capped VBR (`-rc vbr_peak`, `-b:v`, `-maxrate`,
  `-bufsize`) with `-quality speed`, matching the CORE-2089 rate control of the other encoders.
- The hardware-acceleration report lists the `amf` row only on Windows hosts, so Linux and macOS
  reports are unchanged. `accelerator` is an open string on the wire (client-core-wire 2.11.0),
  so no contract change is needed.
- AMF has no hardware tone-map backend; HDR sources on AMF tone-map in software.

## Not in this change

- A Windows probe receipt on real NVIDIA, Intel and AMD hardware. It needs a Windows build of core
  (CORE-1042) and stays open on this ticket.
- Console display of the `amf` row (CORE-2100).

## Impact

- `internal/ffmpeg` (vocabulary, candidates, encoder tables, QSV device init, encode argv).
