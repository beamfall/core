# Web listening controls for audio

Ticket: CORE-2960 (`queue:core:main`). Design: `core-private/docs/design/podcasts.md` §8.2
(NEW-PC08), W5 and §9; ADR-0030 §6; reused by audiobooks (CORE-3012, audiobooks.md §5.3, §5.4).

## Why

Spoken-word listening needs controls the web audio stage lacks. Playback speed exists only as a
per-session select in the settings sheet: the Profile's `playback.speed-default` is never applied
and nothing remembers a speed for one collection. There is no sleep timer, and the server's silence
map (`assets.silenceMap`, ADR-0098 area 2) is advertised but no web code reads it, although the
Profile already has a "trim silence" preference. The OS media controls always skip 10 seconds,
whatever skip interval the listener chose.

## What Changes

- A controls row on the audio stage: Speed, Remove long pauses and Sleep timer.
- **Speed:** a popover with a 0.5×–3.0× slider (0.05 steps) and presets 1.0, 1.2, 1.5 and 2.0.
  Pitch is preserved. The speed roams with the Profile through client prefs: the global value in
  the existing `playback.speed-default` key, and an optional override for the item's container
  Entity (album, series, book …) in `playback.speed.container.<entity ID>`, set with a
  "Use for <container>" toggle. The override wins; turning the toggle off clears it. Any stored
  speed in 0.5–3.0 is honoured, and Profile Settings shows a custom value it has no preset for.
  The key names a generic parent Entity so audiobooks reuse it for per-book speed.
- **Remove long pauses:** a toggle bound to the existing `playback.trim-silence` preference. With
  it on and a silence map present, playback jumps over every silent span of 0.8 s or more. Resume
  offsets stay in file time (the element's own clock), and the row shows the time saved this
  session ("Saved 6m 12s").
- **Sleep timer:** 15, 30, 45 or 60 minutes, end of this item, or end of this chapter (when the
  item has chapters). The last 10 seconds fade the volume out, then playback pauses and the volume
  is restored. End of item pauses just before the end, so nothing auto-advances. The button shows
  the time left while a timer runs.
- **Skip intervals:** the configured interval (global or per container, already in client prefs)
  now also drives the Media Session seekbackward and seekforward actions. Chapter skip buttons stay.
- On the audio stage, the settings sheet's session-only speed select is removed in favour of the
  persisted Speed control. Video keeps it.
- Every control has an accessible name in all five locales.

## Non-goals

- New wire fields: the silence map URL is an existing playback-info field and the speeds are
  client prefs, so no ADR or wire version is needed.
- Volume boost and the show settings sheet (W6), native clients, and separate back and forward
  intervals.
- Listening controls during a Watch Party: the party owns the rate and the timeline, so the
  persisted speed and silence skipping stand down while a party is active.

## Failure modes

- No silence map (not generated yet, or an older server): the toggle still saves the preference
  and the row says nothing was saved; nothing is skipped.
- A malformed silence map or a failed fetch skips nothing.
- An out-of-range or malformed stored speed resolves to the next level down, then 1.0×.
- Blocked or failed pref writes leave the speed in effect for this page only.

## Acceptance evidence

- vitest: `listening.test.ts` (speed parsing and resolution, silence span filtering and jump
  targets, sleep fade and targets).
- `npx tsc -b --noEmit` and `npm run check` in `internal/web/app`.
- Desktop and phone-width screenshots of the controls row and the speed popover.

## Rollback

Revert the change. Stored client prefs are inert without the client code; no server state changes.
