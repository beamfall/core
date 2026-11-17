# Probe a missing media runtime

Ticket: CORE-2314 (`queue:core:main`).

## Why

Some containers (MKV remuxes, some WebM) declare no `format.duration`; ffprobe reports the runtime
only on a stream or in a `DURATION` tag. Such a Media was stored with a 0 runtime, so playback info
reported no duration and an HLS session wrote a growing event playlist that ended where the encoder
was: the player showed a short, growing scrub bar and could not seek past it.

## What Changes

- Probe parsing falls back, in order, to `format.duration`, the longest stream `duration`, the
  longest stream `DURATION` / `DURATION-xx` tag (`HH:MM:SS.frac`), then a format `DURATION` tag. New
  scans store that runtime.
- A Media already stored without a runtime is probed on demand when playback is decided and when an
  HLS session starts. The result is cached by content fingerprint (a miss for 10 minutes), so one
  playback probes the file once. `timeline.durationSeconds` in playback info carries the probed
  value and the session writes a VOD playlist that covers the whole file.
- An Encrypted Library's file is never probed in place (its bytes on disk are ciphertext).
- Persisting the probed runtime back to the stored Media is out of scope (no store setter exists).
