## 1. Migrations and Store

- [x] 1.1 Add catalog migration `0136_player_profile_binding.sql` and events migration `0030_player_commanded_media.sql` and update schema freeze markers
- [x] 1.2 Add `ProfilePlayer` kind, fixed-kind enforcement, PIN/reveal/restricted-library/picker/delete rules in `internal/store`
- [x] 1.3 Add `SetDevicePlayerBinding`, `ClearDevicePlayerBinding`, `SetPlayerWalkUp`, binding generation invalidation, and commanded-media tracking
- [x] 1.4 Enforce bound Player Profile across enrollment, renewal (`RenewDeviceSession`), and re-pair paths

## 2. Capabilities, Auth Scope, and Enforcement

- [x] 2.1 Add `player-display` client capability in `internal/capabilities`
- [x] 2.2 Wire per-request declared client capabilities and device player binding fields into `auth.Scope`
- [x] 2.3 Enforce `updateRequired` gating and walk-up policy in `internal/graph` and `internal/server`

## 3. Graph API, OpenSpec, and Tests

- [x] 3.1 Register `setDevicePlayerBinding`, `clearDevicePlayerBinding`, `setPlayerWalkUp`, `playerBinding`, and `createProfile(kind: "player")` in `internal/graph` and regenerate wire artifacts
- [x] 3.2 Add unit and integration tests for kind fixity, picker exclusion, `switchProfile` refusal, reveal/PIN refusal, generation bump session termination, renewal, `updateRequired`, and walk-up off/on
