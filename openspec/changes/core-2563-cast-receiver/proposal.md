# Resolve the Default Media Receiver so cast playback is served (CORE-2563)

## Why

A `chromecast-` client marks its Stream Token receiver-bound (ADR-0194 §6), and the byte routes
refuse a receiver-bound grant whose receiver never resolved through an ADR-0161 quirks selector. The
shipped quirks baseline was empty, so every cast load was refused, the core web Cast button was
hidden behind `CAST_RECEIVER_PLAYBACK_READY = false`, and Android sidestepped the gate by sending a
client id without the `chromecast-` prefix (CORE-2563, 2026-11-18 Chromecast audit).

## What changes

- The shipped quirks baseline gains `chromecast-default-receiver-floor`, selecting client
  `chromecast-default-receiver` (Google's Default Media Receiver) and pinning its published floor:
  H.264 only, no HDR, at most 720 lines, and AAC only with at most two channels. Every other
  video codec, audio codec and HDR format is denied, so a sender profile broader than the floor
  cannot route past it, whether by direct play or by an E-AC-3 transcode.
- The cap is 720 lines, not 1080. The one client id covers every Cast device, and Google's Cast
  supported-media table lists Nest Hub and Nest Hub Max at 720p. Chromecast and Google TV get
  their 1080p and 4K ceilings only through per-model selectors (CORE-2567).
- `chromecast-default-receiver` is the one client id every sender declares for the Default Media
  Receiver. Android moves to it from `beamfall-cast-default-receiver-v1` (beamfall-android-ui kit).
- Core web drops `CAST_RECEIVER_PLAYBACK_READY` and shows its Cast button whenever the Cast SDK
  finds a device.
- Any other `chromecast-` client still fails closed; per-model receiver selectors and a custom
  receiver are CORE-2567.
