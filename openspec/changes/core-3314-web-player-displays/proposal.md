# Web console, Play on picker, QR resolution and party Screens for player displays

Ticket: CORE-3314 (`queue:core:main`), implementing ADR-0240 D1, D2, D4, D6, D7, D9 and D10 in the
Core Web UI (`internal/web/app`), rollout step 6.

## Why

The server already supports `player` Profiles, Device bindings, controllers, walk-up, presence and
party stage joining (CORE-3309 to CORE-3313), but nothing in the web app can use them. Owners need
to set them up in the console. Household members need to see and command player displays from Play
on, and hosts need to add them to a Watch Party's Screens.

## What Changes

- **Capability declaration:** the embedded web UI contract (`EmbeddedUIContract` and the web
  `CLIENT_CONTRACT`) requests `player-control`, so the server fills `playerDisplays` and
  `availableDisplays` and accepts `partyScreenAddDisplay` for web callers.
- **Console Profiles (D1, D4, D7):** `player` can be chosen when a Profile is created. A Player
  Profile's kind is shown as fixed and never offered on viewers. Owners edit its controllers (All
  adults, plus include and exclude lists) and its walk-up switch. Turning walk-up on asks for
  confirmation with the D7 warning.
- **Console Devices (D2):** owners bind a Device to a Player Profile with a display name, and clear
  the binding.
- **Play on picker (D6, D9):** lists the player displays the caller controls with ready, playing or
  offline presence. A playing display shows its current item, or "Private item" when the caller may
  not see it. An offline display shows "Open Beamfall on <name>" and cannot be chosen. Choosing a
  display sends `play-media` through `controlRendererSession`, retrying once on a revision conflict.
- **QR resolution (D9):** `/web/play-on?display=<handle>` resolves the handle against
  `playerDisplays` after sign-in and opens Play on for that display. An unknown, revoked, unbound or
  uncontrolled handle shows the same "display not available" message. The URL format is documented
  in `contracts/player-profile/v1/README.md` for the native apps.
- **Party Screens (D10):** the host's Screens list offers `availableDisplays` with presence and
  library coverage, adds one with `partyScreenAddDisplay`, marks player-display screens, and can
  make a screen the lead with `partyScreenLead`.

## Non-goals

- Ambient Display settings for player displays (D8): a separate ticket.
- Native apps: tvOS display shell (CORE-3316), senders and QR scanning (CORE-3317).
- Any server or wire change other than the capability declaration.
