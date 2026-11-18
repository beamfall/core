# Waveform in the docked mini player

Ticket: CORE-2404 (`queue:core:main`). Follows CORE-2401 (`web-waveform-seek`).

## Why

CORE-2401 draws a music track's waveform as the full player's seek bar. When the listener docks the
player and browses, the mini player still shows a plain 3px strip, so the waveform disappears.

## What Changes

- The docked mini player's progress strip draws the playing track's waveform as a short peak band
  along its top edge, played peaks in the accent colour and the rest muted.
- It reads the same per-Media peak cache as the full player, so docking needs no new request, and it
  prefetches the next queued track, so advancing while docked does not flash.
- While peaks load it keeps the band shape; when they are unavailable it shows the plain strip. The
  strip stays a labelled `progressbar` and is not interactive, as before.

## Non-goals

- Making the mini strip seekable. Native clients (CORE-2402, CORE-2405).
