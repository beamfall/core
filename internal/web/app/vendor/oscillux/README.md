# Vendored Oscillux v0.2.1 (web)

The Web Viewer's audio visualizer (CORE-1309) runs Oscillux, the only source of visual effects
in Beamfall (ADR-0228). These files are copied unchanged from the Oscillux v0.2.1 release, cut
from rev `6f21c91d1212beacd3b3ca0a4706294b9a559d2b` under `AGPL-3.0-only` (ADR-0233). Core adds no
licence terms to them. `pin.json` records the release assets and their SHA-256 sums.

- `wasm/`: the `oscillux-wasm.tgz` package as released (`@oscillux/core`), with its licence,
  notices and third-party licences.
- `glsl300/`: the subset of `oscillux-glsl300.zip` the viewer draws: `index.json` (the bundle's
  own SHA-256 list for every file kept here), the shared vertex shader, eight visuals, the
  present pass, the seven frame flash limiter passes and `legal/`.
- `visuals.json`: the manifest fields of those eight visuals, copied from
  `shaders/manifest.json` at the pinned rev (its SHA-256 is in the file).

Never edit these bytes. A change belongs in Oscillux; take it here by vendoring a new release
and updating `pin.json`. The host glue that drives them is
`src/areas/viewer/player/visualizer/`.
