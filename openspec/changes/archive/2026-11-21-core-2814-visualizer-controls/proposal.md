# Next/previous visual controls and an Off choice for the web visualizer

Ticket: CORE-2814 (`queue:core:main`).

## Why

The owner asked for a way to move between audio visualizations and to switch the visualizer off
(2026-11-20). Today the visual is picked only from the select in the player settings sheet
(CORE-2368: Rotate, One per album, or a pinned visual), so trying the next visual takes four clicks,
and there is no way to turn the visualizer off: a viewer who wants only the artwork, or a lighter
page, has none.

## What Changes

- The visualizer choice gains `off`. With it the audio stage mounts no visualizer: no WebGL context,
  no audio decode or analysis. The artwork backdrop and card show as on a browser without WebGL2.
  The settings select offers Off.
- Next and previous visual pin the visual after or before the one on screen in the Oscillux catalog
  order, wrapping at the ends. Under Rotate or One per album that is whatever the visualizer shows.
  Under prefers-reduced-motion only calm visuals are stepped to. From Off, either turns the
  visualizer back on to the last visual shown (or the first one).
- The audio transport gains a compact visualizer control: previous visual, an on/off toggle that
  names the visual on screen, and next visual. It sits in the auto-hiding transport, in the row the
  audio stage leaves free above the scrubber, and shows only for audio items where the browser can
  visualize.
- Shortcuts: `V` next visual, `Shift+V` previous, through the shared player shortcut table, so they
  are ignored while typing or using a control and are listed in the `?` sheet.
- Host selection policy only (ADR-0228): switching uses the existing Oscillux switch pass
  (CORE-2219); no effect code is added.

CORE-2234 also asks for selectable transition styles, rotation timing and favourites; those stay on
CORE-2234.

## Impact

- Web app only (`internal/web/app`); no wire, store or contract change.
- New strings in every web locale.
