## 1. Migration and Store

- [x] 1.1 Add catalog migration `0137_player_controllers.sql` (`profile.player_controllers_all_adults`, `device.last_seen_at_ms`, `player_controller` table) and bump `schemaFreezeCatalogMigrations`
- [x] 1.2 Implement `PlayerControllers`, `SetPlayerControllers`, `IsPlayerController`, `PlayerBoundDevices`, `TouchDeviceLastSeenBySession`, and commit-time guarded `ControlRendererSessionForPlayer` / `AppendSessionControlEventWithActivityForPlayer` in `internal/store`

## 2. Auth Filters, Handles, Presence, and Rate Limit

- [x] 2.1 Add `PlayerDisplayHandle` and `ProfilePlaybackFilter` in `internal/auth`
- [x] 2.2 Add SSE `EventStreamHeartbeatObserver` touch on keepalives/writes in `internal/server/events.go` and per-controller `session.control` delivery + D6 redaction
- [x] 2.3 Track display SSE presence (`ready`/`playing`/`offline`, 60 s heartbeat expiry) and per-controller-Profile rate limit (~30 commands per 10 s) in `internal/graph`

## 3. Graph API, Conformance, and Tests

- [x] 3.1 Implement dual-Profile `authorizeSessionControl`, `rendererSessionSnapshot`, `playerDisplays`, `playerControllers`, `setPlayerControllers`, and `ProfileInfo.playerControllers`
- [x] 3.2 Register fields, arguments, error codes/reasons, conformance entries, and regenerate wire goldens and mgmt manifests
- [x] 3.3 Add store, graph, and server tests covering membership, non-leaking refusals, verb table, Private item observation, presence/offline, and rate limiting
