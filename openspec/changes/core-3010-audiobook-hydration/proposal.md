# Wire the audiobook hydration hook (CORE-3010)

## Why
The audiobook plugin derives Person credits, Series membership and Genre links from tags and
folders (`MapHydration`, CORE-3009), but Core never calls it, so books scan with no authors,
narrators, series or genres (ADR-0055 amendment A).

## What changes
- `cmd/beamfall` registers an audiobook `HydrationMapper` that adapts the plugin's plan to
  `app.HydrationPlan`, sets the `audiobook-hydrated` marker and writes no Media fields.
- Person, Series and Genre titles fold, so spelling variants resolve to one Entity.
- `go.mod` pins `github.com/beamfall/plugin-audiobook` v0.1.16.

## Impact
Audiobook Libraries only. No wire or schema change.
