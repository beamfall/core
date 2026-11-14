# Web player keyboard shortcuts and help sheet

Ticket: CORE-1682 (`queue:core:main`).

## Why

The web player answers only Space/K, F, M and the arrow keys. Viewers coming from other media
servers and from video sites expect a fuller set: fixed 10 second jumps, frame stepping, speed,
caption and audio track keys, chapter and queue navigation, number keys that seek by percent, and a
`?` sheet that lists them.

## What Changes

- Add a pure shortcut table (`playerShortcuts.ts`) that maps a key event to a player action, plus a
  `usePlayerShortcuts` hook that owns the window listener and applies the actions to the `<video>`,
  the live track lists, the chapter markers and the queue callbacks.
- Add a `?` help sheet listing every shortcut, built from the same table so the two cannot drift.
- `PlayerTransport` swaps its inline keydown map for the hook and renders the sheet.
- Strings for the sheet in every shipped locale.

## Non-goals

- Rebindable keys or a shortcuts settings page.
- Subtitle offset and audio delay keys (CORE-1671) and a chapter list (CORE-1683).
- Shortcuts outside the player.
