# Vendored Oscillux v0.2.15 (web)

The Web Viewer's audio visualizer (CORE-1309) and photo slideshow (CORE-2980) run Oscillux, the only source of visual effects
in Beamfall (ADR-0228). These files are copied unchanged from the Oscillux v0.2.15 release, cut
from rev `646b841912f268faedd0809dc0b22e27bb710c1d` under `AGPL-3.0-only` (ADR-0233). Core adds no
licence terms to them. `pin.json` records the release assets and their SHA-256 sums.

- `wasm/`: the `oscillux-wasm.tgz` package as released (`@oscillux/core`), with its licence,
  notices and third-party licences.
- `glsl300/`: the subset of `oscillux-glsl300.zip` the viewer draws: `index.json` (the bundle's
  own SHA-256 list for every file kept here), the shared vertex shader, every visual (all 61 `.web.frag` legs), the
  present pass, the switch pass, the six frame flash limiter passes, the photo slideshow pass
  (`slideshow/slideshow.frag`) and `legal/`.
- `visuals.json`: the fields of every visual in the release set the shader legs ship, in the
  catalog's `catalogOrder`: from `shaders/manifest.json`, with the header, palette, trail decay,
  params and post of each rebuilt original in `shaders/originals/manifest.json` that overrides it,
  at the pinned rev (both SHA-256s are in the file).

Never edit these bytes. A change belongs in Oscillux; take it here by vendoring a new release
and updating `pin.json`. The host glue that drives them is
`src/areas/viewer/player/visualizer/` and, for the slideshow pass,
`src/areas/slideshow/lib/slideshowMotion.ts`.
