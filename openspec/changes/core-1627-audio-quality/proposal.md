# Audio quality: sample rate, bit depth and lossless delivery

Ticket: CORE-1627 (`queue:core:main`). Clients show the quality in their own repositories.

## Why

A listener with a hi-res library wants to know whether the player hears the source as recorded:
its codec, sample rate and bit depth, and whether the stream reaches the device unchanged. Core
recorded bit depth but read the sample rate only in the gapless pass for audio-only files, a client
could not state a hi-res ceiling (a device that renders 48 kHz was handed a 192 kHz FLAC to direct
play), and playback info said nothing about the delivered audio.

## What Changes

- **Probe**: ffprobe's `sample_rate` is recorded for every audio stream, bounded like the other
  numeric probe fields. `media_file_stream.bit_depth` (catalog 0050) and `sample_rate` (catalog
  0118, encrypted-library 0006) already exist and the scan already maps both, so no migration is
  needed.
- **Capability profile**: `audioCodecs[]` gains optional `maxSampleRate` (Hz) and `maxBitDepth`
  (bits per sample). 0 or omitted means no ceiling. A ceiling is checked only when the probe
  recorded the source's value.
- **Decision**: a source audio stream above either ceiling is neither direct-played nor passed
  through; the decision falls to the next rung (a transcode or audio transcode).
- **Wire**: `playbackInfo` gains optional `audioQuality {codec, sampleRate, bitDepth, lossless,
  delivery}` and `next.audioQuality`. `delivery` is the closed vocabulary `direct|transcode`:
  direct means `streams[0]` carries the source audio bit-for-bit (direct play, or a live transcode
  copying a passthrough track). The change is additive; wire contract 2.11.0 is published and the
  registry's active version stays at 2.10.0 until every consumer re-vendors.

## Out of scope

- Re-pinning the consumers (apple-ui, android, android-ui, web) and the registry's active version.
- The web UI that shows the quality (CORE-1636) and the native client UIs.
- Resampling a transcode to the declared ceiling rather than the encoder's default rate.
- The v1 developer API capability subset, which keeps its existing fields.
