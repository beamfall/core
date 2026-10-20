# Ambient Group sync spec

Status: accepted
Date: 2026-06-23

Realizes: ADR-0035 Decision 8.

This spec owns the implementation constants and protocol details for LAN-only Ambient Group audio
sync. ADR-0035 owns the binding product rule: grouped Ambient Display sessions share a
beat-accurate audio timeline, fail closed, and never require synchronized video.

## Join calibration

When a device joins an Ambient Group it performs a four-timestamp NTP-style round trip with the
server. The server records:

- **Clock offset** between device-local time and server time.
- **Render latency** reported by the platform audio stack after first decoded byte reaches the
  speaker path.

These values are member-local and expire when the session, Profile authority, device grant, or
network path changes.

## Transition protocol

For skip, natural track end, pause, resume, or join-in-progress:

1. The server picks a future NTP presentation time, normally `now + 2s`.
2. The server broadcasts `{ item, position, present_at }` over each member's session event channel.
3. Each member buffers immediately and presents at `present_at - render_latency`.
4. Members that cannot buffer or present inside the window fail closed out of the group.

The two-second pre-buffer is a starting target, not an ADR constant. Implementations may tune it if
verification proves the group still stays beat-accurate on supported LANs.

## Drift correction

Members periodically report `{ position, local_ntp_time }`. The server compares reported position
with the expected position at that NTP time.

- Small drift triggers a micro-correction by bounded playback-rate adjustment.
- Large or stale drift triggers a full transition-protocol re-sync.
- A member that keeps missing the recoverable window leaves or is removed from the group.

Current target values are roughly `10ms` for micro-correction and `500ms` for unrecoverable drift.
They are implementation tuning values, not product vocabulary.

## Clock sync

Ambient Groups assume LAN NTP-class clock quality. PTP / IEEE 1588 is not required for v1 because it
demands host support Beamfall cannot assume. WAN sync is out of scope until a future ADR accepts the
buffering, authority, and user-experience tradeoffs.

