# Player Profile kind, durable Device binding, and walk-up policy

Ticket: CORE-3310 (`queue:core:main`), implementing ADR-0240 D1, D2, and D7 (core server only).

## Why

Shared household screens (the karaoke room TV, the lounge TV, the garage display) currently have
to sign in as a household member's Profile, putting that person's rows, history, and Continue
Watching on a shared screen and preventing other household members from driving it. ADR-0240 defines
a dedicated `player` Profile kind, a durable `Device` player binding with generation-based session
invalidation, per-connection `player-display` capability enforcement, and server-enforced walk-up
policy.

## What Changes

- **Profile kind `player` (ADR-0240 D1)**:
  - Add `ProfilePlayer` (`"player"`) to `ProfileKind`, creatable only by the owner capability.
  - Enforce kind fixity: adult/managed Profiles cannot become `player`, and a `player` Profile
    cannot change kind.
  - Player Profiles never reveal, accept no PIN (neither reveal PIN nor entry PIN), never receive
    restricted Libraries, are excluded from picker listings, and `switchProfile` into one fails
    with the same `"profile not found"` refusal as an unknown Profile.
  - Deleting a Player Profile revokes every Device bound to it and ends their Sessions.
- **Durable Device player binding (ADR-0240 D2)**:
  - New catalog migration (`0136_player_profile_binding.sql`) adding nullable `player_profile_id`,
    `player_display_name`, and `player_binding_generation` to `device`, and `walk_up_enabled`
    (default false) to `profile`.
  - New events migration (`0031_player_commanded_media.sql`) recording Media IDs named by
    `play-media` session-control commands addressed to player-bound Sessions.
  - Store methods `SetDevicePlayerBinding`, `ClearDevicePlayerBinding`, and `SetPlayerWalkUp`
    (exposed via owner-only GraphQL mutations `setDevicePlayerBinding`,
    `clearDevicePlayerBinding`, `setPlayerWalkUp`). Setting, changing, or clearing a binding
    increments `player_binding_generation` and atomically ends every Session of that Device,
    clearing outstanding stream-token grants and pending session-control commands.
  - Every session-issuance and renewal path (`EnrollDevice`, pairing approval/redemption,
    `RenewDeviceSession`, re-pair) issues Sessions for a bound Device solely on its bound Player
    Profile; `switchProfile` from a bound Device Session is refused.
- **Client capability gating (ADR-0240 D2, ADR-0138)**:
  - Register the `player-display` client capability in `internal/capabilities` and surface
    per-request declared client capabilities from the compatibility decision into `auth.Scope`.
  - When a bound Device's Session did not declare `player-display`, only `playerBinding` is
    readable; library, browse, search, layout, playback, and control operations fail with a typed
    `FORBIDDEN` / `UPDATE_REQUIRED` (`"update required"`) error.
- **Server-enforced walk-up policy (ADR-0240 D7)**:
  - With `walk_up_enabled` off (default), a bound display Session is refused Library browse, search,
    and Layout reads, and may read Media only when a `play-media` session-control command addressed
    to that Session named the Media within the last 12 hours.
  - With `walk_up_enabled` on, normal reads proceed under the Player Profile's Library access
    list while Reveal, Profile picker, PIN, and settings remain refused.

## Non-goals

- Revision-checked cross-Profile Renderer Session control (ADR-0240 D3/D4/D6/D9), originator
  engagement attribution (D5/D8), party stage eligibility (D10), web Console UI, and native clients.
