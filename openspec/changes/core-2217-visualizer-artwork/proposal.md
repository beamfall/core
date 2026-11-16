# Feed the track's artwork to the Web Viewer visual's image input

Ticket: CORE-2217 (`queue:core:main`).

## Why

The owner asked for the visualizer's colours to come from the artwork (2026-11-16). Several pinned
Oscillux visuals (Drift, Lens, Liquid Optics among them) already read an image input and use the
artwork as a tint, but the Web Viewer's renderer binds a 1x1 neutral grey stand-in to `uImage`, so
those visuals never see the artwork. Artwork-derived palettes for every visual are the separate
PALETTE0927 chain (CORE-0178 to CORE-0183) and stay out of scope.

## What Changes

- The audio stage passes its artwork URL to the visualizer.
- The visualizer fetches that artwork once per URL (same origin only, with the page's credentials),
  decodes it downscaled, and the renderer uploads it as the visual's image input. It swaps on track
  change and keeps the neutral stand-in when there is no artwork, the artwork is cross-origin, or
  the fetch or decode fails.
- Host glue only: no effect code is added to core (ADR-0228). How the image colours a frame stays
  with the pinned Oscillux shaders.

## Impact

- Web app only; no wire, store or contract change.
