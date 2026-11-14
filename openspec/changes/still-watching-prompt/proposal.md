# "Are you still watching?" prompt

Ticket: CORE-1689 (`queue:core:main`), from the Jellyfin client-parity audit of 2026-11-14.

## Why

Up Next autoplays the next queued item with nobody there to stop it. A player left running overnight
works through a whole queue, keeps a transcode busy and marks items watched that nobody saw. Other
media servers stop after a few autoplayed items and ask. Beamfall clients have no such stop, and no
shared setting a Profile could use to tune it.

## What Changes

- A new per-Profile client preference, `stillwatching.after`, in the existing generic
  `profile_client_prefs` store (ADR-0014). Values: `1`..`10`, or `off`. Absent or malformed means 3.
  The server stores it as an opaque key like every other client preference; no server change.
- Every launch player keeps a streak of items in a row that the Up Next countdown started on its own.
  Any viewer input, Play now, picking an item, or Continue on the prompt resets it.
- When an item ends with a playable next item and the streak has reached the setting, the player
  stops and shows the prompt instead of autoplaying. Up Next is hidden while the prompt is armed.
- Paths with no countdown (the web player's audio auto-advance, a continuous audio queue) neither
  count nor prompt.
- The web viewer gets the prompt and a "Still watching" row on Profile settings. The Apple and
  Android clients implement the same rules in their own repositories under the same ticket.
- `docs/still-watching/README.md` records the cross-client rules.

## Non-goals

- Syncing the streak between devices or across reloads; it is in memory only.
- Server-side enforcement or a server-issued stop.
- Auto-skip of media segments; the iPhone half of CORE-1689 lives in `beamfall-apple`.

## Capabilities

### New Capabilities

- `still-watching-prompt`: when a player stops autoplay to ask, and the shared setting that decides it.

### Modified Capabilities

None.
