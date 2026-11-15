# Viewer streaming ceiling

Ticket: CORE-1674 (`queue:core:main`).

## Why

A viewer on a phone cannot keep playback inside a data allowance, and a viewer on a slow link
cannot stop the server from offering a quality the link cannot carry. The capability profile says
what a device can decode, not what the viewer wants to spend. Clients need one field to say
"stay at or under this bitrate", and the playback info needs to say when that choice, rather than
the device, caused a transcode.

## What Changes

- `CapabilityProfile` gains an optional `maxStreamingBitrate` (bits per second). Zero or absent
  means no ceiling. Clients choose the value; for example, Android sends a Wi-Fi or cellular ceiling
  depending on the active network.
- Playback negotiation applies the ceiling through the same paths as a device quirk's bitrate cap:
  a source whose bitrate exceeds the ceiling, or whose bitrate is unknown, is not handed over as
  direct play, direct stream or audio copy, and transcode rungs above the ceiling are dropped. A
  ceiling below the lowest rung leaves the lowest rung. Audio-only items are exempt, since they
  have no picture to scale.
- When the ceiling alone forced the transcode (the source would otherwise direct play, direct
  stream or copy its audio), the playback info reason is `bitrate-ceiling`. Any other refusal
  keeps its own reason. The dashboard's play reason reads `bitrate`, not `device-quirk`.
- The ceiling is part of the capability fingerprint, so sessions under different ceilings never
  share renditions.
- Wire contract 2.5.0 adds the field; the registry repoints to it.

## Impact

- `internal/playback` (decision, quirk overlay, observation), wire contract goldens and artifact,
  `contracts/registry.json`.
- The store does not read media file sizes back yet (CORE-0555), so every source bitrate is
  currently unknown at negotiation time and any ceiling transcodes. That errs on the side of the
  viewer's data cap. CORE-0555 makes the ceiling precise.
- CORE-1717 (server-side per-profile cap) can combine with this field by taking the lower of the
  two.
