# Gapless playback: encoder delay and padding

Ticket: CORE-1626 (`queue:core:main`). Client players apply the trim in their own repositories.

## Why

Lossy encoders prime their output with decoder warm-up samples (the encoder delay) and pad the last
frame out to a whole frame (the end padding). A player that trims neither leaves a short silence
between two consecutive items cut from one continuous recording. Core recorded neither value, and
the live HLS transcode dropped the AAC priming at its first segment (an input `-ss 0` discards the
source's first packet), so even a player that knew the source's values could not trim the
transcoded stream exactly.

## What Changes

- **Probe**: for audio-only sources, a second, bounded ffprobe pass reads the packet side data
  FFmpeg reports for MP3 (LAME/Xing header), AAC/M4A (edit list) and Opus (pre-skip): the first
  packet's `skip_samples` is the delay and the last packet's `discard_padding` is the padding. It
  reads the first packets and the last seconds only. An iTunes `iTunSMPB` tag overrides the first
  audio stream. The probe also measures the decoded content length.
- **Store**: `media_file_stream` gains `sample_rate`, `encoder_delay_samples`,
  `encoder_padding_samples` and `decoded_samples` (catalog migration 0118, encrypted-library
  migration 0006), filled by the scan.
- **Transcode**: a live encode of a source without a picture, started from the beginning, no
  longer passes `-ss 0`, so the AAC encoder's 1024-sample priming and its padding are exactly
  predictable from the content length. The fMP4 edit list stays at media time 0: the container
  trims nothing.
- **Wire**: `playbackInfo` `streams[].gapless {sampleRate, encoderDelaySamples,
  encoderPaddingSamples}` is an additive, optional field. Direct play carries the source's values
  when it declares them; a live audio transcode started from the beginning carries the encoder's
  values. The client trims once; nothing else trims.

## Out of scope

- Gapless for resumed or far-seek-restarted live transcodes (each restart re-primes), audio-copy,
  precomputed, pitch/tempo-adjusted and composed-picture streams: these carry no `gapless` field.
- Publishing the wire contract version that carries the field (the batch lead publishes).
- The client-side trim in each player.
