# Tidy the web audio player: settings rows, a seekable mini bar, radio tuning in the bar

Ticket: CORE-3340 (`queue:core:main`).

## Why

Owner feedback on 2026-11-23. Speed, Remove long pauses and Sleep timer sat as chips on the audio
stage and crowded it. The docked mini player's progress line could not be clicked to seek, and its
Next button did not name the next track as the full player's does. The radio tuning selects were
pinned to the top left of the stage, where they overlapped Back.

## What Changes

- On the audio stage, Speed, Remove long pauses and Sleep timer become rows in the transport's
  Settings sheet. Their popovers open inside the sheet.
- The mini player's progress strip becomes a seek slider: click or drag to seek, with arrow, page,
  Home and End keys. It keeps the waveform strip.
- The mini player's Next button shows the next title on hover and includes it in its accessible
  name.
- The mini bar's spacing is refined, and a short fade sits above it.
- The radio tuning selects move into the player bar beside Back and are restyled as pills.

## Impact

Web app only: `internal/web/app/src/areas/viewer` (player, pages, components). No wire, schema or
server change.
