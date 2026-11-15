## 1. Store

- [x] 1.1 Migration 0117 adds `profile.remote_max_bitrate` (default 0); `Profile.RemoteMaxBitrate` and `ProfileSettings.RemoteMaxBitrate`
- [x] 1.2 Server configuration key `playback.remote_max_bitrate`; `ConsoleConfig.RemoteMaxBitrate` and its patch field
- [x] 1.3 Validation: whole bits per second from 0 to 1,000,000,000
- [x] 1.4 Tests: round-trip, clearing with 0, unrelated writes preserve the cap, out-of-range refusals

## 2. Admin API

- [x] 2.1 `remoteMaxBitrate` on `consoleConfig` / `setConsoleConfig` and on profile reads, `createProfile` and `updateProfile`
- [x] 2.2 Tests: admin round-trip, invalid values refused, manager without an admin session and viewer refused
- [x] 2.3 Regenerate the wire goldens

## 3. Playback

- [x] 3.1 The server marks playback-info requests remote from the connection (`/api/playback-info` and the v1 route)
- [x] 3.2 Remote requests play under min(client ceiling, server cap, profile cap); LAN requests ignore the policy cap
- [x] 3.3 Reason `policy-bitrate-ceiling` when the policy cap alone forced the transcode
- [x] 3.4 Tests: decision table (LAN, no cap, server cap, profile cap, either lower, client lower, other refusal) and the route-level remote marking

## 4. Console

- [x] 4.1 Server-wide cap in Remote access; per-profile cap in the managed profile policy controls (Mbit/s)
- [x] 4.2 Vitest: GraphQL client operations and both controls
