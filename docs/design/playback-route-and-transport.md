# Beamfall playback route and transport state board

Status: draft v0.1  
Date: 2026-06-11

This document defines the shared visual evidence required for the custom transport and the
playback route explanation pattern. It applies to Web UI, tvOS, Android TV, Android mobile,
iPhone, iPad, and macOS Viewer surfaces.

## Principle

The player must be honest without becoming a codec dashboard. It names the route in plain
language first, then exposes technical detail one layer deeper.

## Route summary pattern

Every player surface has a compact route summary with these fields:

| Field | Examples | Notes |
|---|---|---|
| Current route | Direct-play, direct-stream, server transcode | Always visible in player chrome or one tap away on TV. |
| Engine | AVPlayer, ExoPlayer, libmpv, HLS | Use platform names only when useful. |
| Reason | "This device cannot play the selected audio track." | Plain-language explanation, not raw codec first. |
| Quality | 1080p SDR, 4K HDR10, Auto | Show manual pin when active. |
| Network path | LAN, direct remote, overlay, Self-hosted Relay | Hosted Relay only after feature graduation. |
| Session state | Active, token expired, relocked, device revoked | Token and Reveal failures must be explicit. |
| Device note | "Passthrough unavailable on this device." | Pulls from Capability Profile and quirks table. |

Raw stream details such as codec, channel layout, bitrate, subtitle renderer, and Dolby Vision
profile live behind an info affordance.

## Required transport controls

- Play/pause.
- Seek backward and forward 10 seconds.
- Scrubber with thumbnail previews.
- Runtime and remaining time.
- Chapter/Marker strip.
- Skip segment actions.
- Audio track menu.
- Subtitle track menu.
- Subtitle styling and sync where supported.
- Quality menu: version, route, auto/manual.
- Route/engine status.
- Up Next and autoplay countdown.
- Info overlay.

## Required player states

Each platform state board must capture:

- Direct-play start.
- Direct-stream/remux start.
- Server transcode start.
- Transcode admission blocked or queued.
- Manual quality pinned.
- Remote path degraded.
- Token expired.
- Session relocked.
- Device revoked.
- Track unavailable on current engine.
- Engine fallback from native player to libmpv.
- AirPlay/Cast/PiP unavailable-honest state where applicable.

## Platform interaction requirements

### TV remotes

- First focus lands on play/pause or the scrubber according to platform convention.
- Left/right seek and scrub do not trap focus.
- Long-press track/quality menus have a visible escape path.
- Scrub thumbnail previews are readable at 10 feet.

### Touch

- All gestures are shortcuts; all functions remain reachable with visible controls.
- Scrubbing exposes thumbnail preview and spoken position.
- PiP, AirPlay, Cast, and Beam limitations are explained at the action point.

### Keyboard and pointer

- Space toggles play/pause when player controls are focused.
- Arrow keys adjust scrubber or move focus according to platform convention.
- Menus expose selected state and disabled reasons.

### Screen readers

- Scrubber role is adjustable.
- Current time, duration, buffered state, and chapter/Marker are spoken.
- Skip actions announce what was skipped.
- Route changes announce only when user-relevant; do not spam codec transitions.

## Screenshot packet

For every platform implementation, capture:

- Idle chrome hidden and visible.
- Focused scrubber.
- Open track menu.
- Open quality/route menu.
- Scrub thumbnail preview.
- Token expired or relocked state.
- Engine fallback or transcode explanation.
- Reduced-motion state if transport animations differ.
