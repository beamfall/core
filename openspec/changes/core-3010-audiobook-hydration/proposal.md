# Wire the audiobook hydration hook (CORE-3010)

## Why
The audiobook plugin derives Person credits, Series membership and Genre links from tags and
folders (`MapHydration`, CORE-3009), but Core never calls it, so books scan with no authors,
narrators, series or genres (ADR-0055 amendment A).

## What changes
- `cmd/beamfall` registers an audiobook `HydrationMapper` that adapts the plugin's plan to
  `app.HydrationPlan`, sets the `audiobook-hydrated` marker and writes no Media fields.
- A multi-part Media (numbered or disc files) hydrates once from its first part, when that
  part was probed in the pass; the scan previously never hydrated part groups.
- `go.mod` pins `github.com/beamfall/plugin-audiobook` v0.1.17.

## Impact
Audiobook Libraries, and part groups of any plugin with a hydration hook. No wire or schema
change.
