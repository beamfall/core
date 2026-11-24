# Player Profile wire contracts, fixtures and SDK decoders

Ticket: CORE-3315 (`queue:core:main`), implementing ADR-0240 D11 (wire, contracts and compatibility).

## Why

ADR-0240 D1–D10 added Player Profile fields to the wire (`playerBinding`, `playerControllers`,
`playerDisplays`, `controlRendererSession`, `PartySessionInfo.availableDisplays`,
`partyScreenAddDisplay`), but nothing tells an older controller apart from one that understands
player displays, and native SDKs have no fixtures to decode them against. D11 requires player rows
only for clients that declare `player-control`, an unchanged listing for older controllers, and
core-owned fixtures with native decoders in `beamfall-apple-ui` and `beamfall-android-ui`.

## What Changes

- **Capability:** add `player-control` to the closed client capability vocabulary next to
  `player-display` (`internal/capabilities`).
- **Older controllers (D11):** without `player-control`, `playerDisplays` and
  `PartySessionInfo.availableDisplays` return no rows, `controlRendererSession` against a player
  display refuses as an unknown session, and `partyScreenAddDisplay` refuses as an unknown display.
  A test pins that an older controller's listings are unchanged.
- **Contract:** new registry boundary `player-profile-wire` (1.0.0) at
  `contracts/player-profile/v1/fixtures.json`, consumed by `beamfall-apple-ui` and
  `beamfall-android-ui`. A conformance test decodes every fixture into the core response types with
  unknown fields refused and re-encodes it byte-for-byte, so the fixtures cannot drift from the
  server.
- **SDK decoders:** BeamfallKit (`beamfall-apple-ui`) and `:kit` (`beamfall-android-ui`) vendor
  the fixtures and decode them in conformance tests. These land on the consumers' mains before the
  core registry entry, per ADR-0122.

## Non-goals

- Web, tvOS, Android TV and phone UI (CORE-3314, CORE-3316, CORE-3317).
- Gating `playerBinding` (a display reads its own binding; D2's `player-display` check covers it)
  or `playerControllers` (an admin console read, not a controller listing).
