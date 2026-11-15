# Remote bitrate limits

Ticket: CORE-1717 (`queue:core:main`).

## Why

The viewer's own streaming ceiling (CORE-1674) is the client's choice. An admin whose upload link
is narrow, or who wants to keep one profile's remote viewing cheap, has no way to stop a client
from asking for more than the link can carry. The server needs its own cap for playback away from
the local network, set once for the whole server and optionally per profile, without slowing LAN
playback.

## What Changes

- Server configuration gains `remoteMaxBitrate` (bits per second, 0 = unlimited), read and written
  through the admin-only `consoleConfig` / `setConsoleConfig` operations.
- Each profile gains `remoteMaxBitrate` (bits per second, 0 = unlimited), written through the
  admin-only `createProfile` / `updateProfile` operations and returned on profile reads. Migration
  0117 adds the column, defaulting to 0.
- Both values are validated: whole numbers from 0 to 1,000,000,000. Anything else is refused and
  nothing is stored.
- The server marks a playback-info request remote from its connection (the same LAN test the
  server already uses: loopback, private and link-local peers are local; relay traffic and
  unresolved forwarded requests are remote). For a remote request, the policy cap is the lower of
  the non-zero server and profile caps, and the effective ceiling is the lower of that and the
  client's `maxStreamingBitrate`. It is applied through the same ceiling path CORE-1674 added, so
  direct play, direct stream, audio copy and the rendition ladder all honour it. LAN requests are
  unaffected.
- When the policy cap is strictly tighter than the client's ceiling and forced the transcode, the
  playback info reason is `policy-bitrate-ceiling`. A client ceiling at or below the policy cap
  keeps `bitrate-ceiling`, and any other refusal keeps its own reason.
- The Console exposes the server-wide cap in Remote access and the per-profile cap in the managed
  profile policy controls, both entered in Mbit/s.

## Impact

- `internal/store` (migration 0117, profile and server configuration), `internal/graph`
  (admin operations), `internal/playback` (decision), `internal/server` (remote marking), the
  Console web app, and the wire goldens (additive `remoteMaxBitrate` fields on the profile and
  console configuration entities).
- No new wire contract version is published here; the additive fields ride the next publish.
- The capped profile is part of the capability fingerprint, so remote and LAN sessions under
  different effective ceilings never share renditions.
