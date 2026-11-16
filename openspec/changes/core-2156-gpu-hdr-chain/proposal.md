# GPU decode, scale and tone-map for HDR HEVC transcodes on Intel

Tickets: CORE-2156 (`queue:core:main`), a slice of CORE-0024

## Why

A 4K HDR transcode on an Intel i7-8700K ran at 0.90x realtime: the encoder used the GPU, but
decode, scale and tone-map ran on the CPU. The same source runs at 1.54x when decode, scale and
tone-map stay on the GPU. Building that showed the hardware tone-map could never be certified on
real hardware: its control leg asked for a download format FFmpeg refuses, the OpenCL tone-map did
not match the software picture, and the server image had no Intel OpenCL driver.

## What changes

- A new device-decode chain for HEVC PQ and HLG sources on Linux when the verified encoder is QSV
  or VA-API: VA-API decode, `scale_vaapi` to 10-bit at the rendition height, OpenCL tone-map,
  then a reverse map onto the encoder's device. No frame visits system memory.
- The chain is used only after a probe certifies it on the host: it decodes a small HEVC Main10
  sample on the device and compares the tone-mapped picture with a decoded pass-through control,
  with the floors ADR-0075 already sets. The verdict is cached per backend, transfer class and
  FFmpeg build.
- Sources that are not eligible (other codecs, Dolby Vision profile 7 or 8, subtitle burn-in,
  interlaced, non-square pixels, composed picture) and hosts where the probe fails keep exactly
  today's arguments.
- The OpenCL tone-map leg sets `tonemap_mode=max`, which tracks the software reference within 7
  code values on the probe fixture at three saturation levels (the default left highlights 55
  code values brighter).
- The pass-through control of the existing upload chain downloads in the upload format, so it
  runs on real hardware.
- The server image installs `intel-opencl-icd`.

## Out of scope

NVENC and VideoToolbox device decode, Dolby Vision device decode, H.264 and AV1 sources, and the
rest of CORE-0024 (SDR hardware decode). The FFmpeg pin manifest does not yet record
`scale_vaapi` and `hwmap` help; the host probe fails closed if either is missing.
