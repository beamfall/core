# Waveform seek bar for music in the web player

Ticket: CORE-2401 (`queue:core:main`).

## Why

Core already computes a per-file waveform envelope (`waveform.json`, 1000 peaks normalised to
[0,1]) as a Generated Asset, but the web player never draws it: music plays against the same plain
bar as video. The envelope is also only produced by the background asset sweep, which can take days
on a large library, so even a client that drew it would show nothing for most tracks.

## What Changes

- The playback decision for an audio-class Media always advertises `assets.waveform`, whether or
  not the envelope has been generated yet. Other classes are unchanged: they advertise it only when
  the file exists.
- The stream-token asset route generates a missing waveform on demand for that request, under a
  per-fingerprint single-flight and a small concurrency limit, then serves it. If generation fails
  or the Media has no usable audio source, the route answers 404 as before.
- The web player draws the waveform as the seek bar for audio: played and unplayed peaks in
  different colours, click or drag to seek. It keeps the existing slider (role `slider`, keyboard
  seek, `aria-valuetext`), so accessibility does not change.
- While peaks load, or when they are unavailable, the plain bar shows. Peaks are cached per Media
  in the page, and the next queued track's waveform is fetched ahead, so the bar does not flash or
  reset between songs. The bar fits a 375px-wide viewport.

## Non-goals

- The docked mini player's thin progress strip and native clients. Follow-ups.
- Changing the envelope format or the sweep. A peak summary for a seek bar is UI data, not a visual
  effect; Oscillux has no reusable peak code, so no effect code moves into core.

## Failure modes

- On-demand generation is bounded: a burst of waveform requests waits on at most two FFmpeg runs,
  and a failed run returns 404, so the client stays on the plain bar.
