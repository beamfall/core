# Player controllers list, dual-Profile authorization, observation, and presence

Ticket: CORE-3311 (`queue:core:main`), implementing ADR-0240 D4, D6, and D9 (core server only).

## Why

With `player` Profiles and durable `Device` bindings in place (CORE-3310), household members need
to discover and control shared displays from their own devices without borrowing someone else's
Profile or leaking restricted Media. ADR-0240 D4, D6, and D9 define the live controllers list on
Player Profiles, dual-Profile verb authorization, field-minimal observation, and SSE-plus-heartbeat
presence.

## What Changes

- **Controllers list on Player Profiles (ADR-0240 D4)**:
  - New catalog migration (`0137_player_controllers.sql`) adding
    `player_controllers_all_adults` (default `1`) to `profile`, `last_seen_at_ms` (default `0`) to
    `device`, and the `player_controller(player_profile_id, profile_id, mode)` side table with
    `ON DELETE CASCADE` in both directions.
  - Store methods `PlayerControllers`, `SetPlayerControllers`, and `IsPlayerController` evaluated
    live from the caller's current Profile kind (`include`, or `allAdults` and kind `adult`; never
    if in `exclude`; `player` Profiles never).
  - Owner-only GraphQL mutation `setPlayerControllers` and query `playerControllers`, plus
    owner-visible `playerControllers` on `ProfileInfo`.
- **Dual-Profile authorization & commit-time binding re-check (ADR-0240 D4)**:
  - `authorizeSessionControl` replaces the same-Profile check with `IsPlayerController` when the
    target Session belongs to a Player Profile; non-controllers receive `"unknown session"` for every
    outcome (nonexistent, unbound, offline, stale revision, replayed/reused `commandID`).
  - Commit-time re-check under `sessionBindMu` verifies that the target Session still belongs to the
    bound Player Profile at the expected `playerBindingGeneration` and that the caller remains a
    controller.
  - Verb authorization enforces `PlaybackFilter()` on both caller and Player Profile for
    `play-media`; controller-only for `stop` and `display-message`; caller `LibraryFilter()` on the
    current item for `pause`, `seek`, `set-volume`, `mute`, `unmute`, `select-audio-track`,
    `select-subtitle-track`, `skip-track`, and `skip-media`; and caller `PlaybackFilter()` on the
    current item for `resume`.
- **Audit & rate limit (ADR-0240 D4)**:
  - Cross-Profile commands write `session.control` activity rows attributed to the controller
    Profile and structured audit logs with controller Profile, display session/device, verb, and
    result, and enforce a per-controller-Profile rate limit (~30 commands per 10 s).
- **Observation & presence (ADR-0240 D6, D9)**:
  - Per-observer D6 field filtering on `rendererSessionSnapshot`, `playerDisplays`, conflict
    snapshots, and SSE `session.control` delivery: visible items expose `mediaID`, `title`,
    `libraryID`, `artworkURL`, `positionSeconds`, and `durationSeconds`; invisible items render as
    `"Private item"` with `positionSeconds` and `durationSeconds` only.
  - New `playerDisplays` listing query returning controlled displays with opaque HMAC handles,
    display name, `ready`/`playing`/`offline` presence, `lastSeenAt`, and D6-filtered `currentItem`.
  - SSE connection and keepalive/heartbeat tracking marks displays `offline` after 60 s without a
    heartbeat and persists `last_seen_at_ms`; commands to offline displays return typed
    `CONFLICT`/`DISPLAY_OFFLINE` (`"display offline"`) to controllers and `"unknown session"` to
    non-controllers.

## Non-goals

- Originator engagement attribution and Ambient Display engagement silence (ADR-0240 D5/D8).
- Watch Party per-screen stage eligibility (ADR-0240 D10).
- Queue append/insert/remove/move and transfer verbs for Renderer Sessions (not present in core).
- Web Console UI and native clients.
