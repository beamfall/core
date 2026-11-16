# Third-party notices

Short third-party code snippets used in Oscillux's shader library, with the terms their authors
published. Everything here was read from the author's own page on 2026-11-14 (UTC). Oscillux's own
source is under `AGPL-3.0-only` (see [LICENSING.md](LICENSING.md)).

## Verified from the author's own page

### ACES filmic tone-mapping curve fit

- What: the rational curve fit `(x*(2.51*x+0.03))/(x*(2.43*x+0.59)+0.14)`.
- Author: Krzysztof Narkowicz.
- Source: <https://knarkowicz.wordpress.com/2016/01/06/aces-filmic-tone-mapping-curve/> (post dated
  2016-01-06; fetched 2026-11-14).
- Licence as published: the author's text says the fitted curve's HLSL source code is "free to use
  under public domain CC0 or MIT license" (a choice between the two). The page publishes no
  copyright line and no MIT permission text, so there is no further notice text to reproduce.
- Used in: `shaders/library.glsl` (`tonemap`), `shaders/present/present.glsl` (`presentAces`).
  The same coefficients also appear in reference models and checkers: `tools/shader/present-model.mjs`,
  `tools/shader/render.mjs`, `tools/shader/beat-lint.mjs`, `tools/shader/cdg-check.mjs`,
  `tools/shader/switch-transition-check.mjs`, `tools/shader/test-present-v2.mjs`,
  `tools/shader/metal-smoke.swift`, `tools/switch-transition-vectors.py`.

### Cosine colour palette

- What: the palette formula `a + b * cos(2*pi*(c*t + d))`.
- Author: Inigo Quilez.
- Formula source: <https://iquilezles.org/articles/palettes/> (fetched 2026-11-14).
- Licence source: <https://iquilezles.org/articles/> (fetched 2026-11-14), the index of the same
  article set. Licence as published: "all technical code snippets you'll find are under the MIT
  license". The same page adds that the mathematical/shader art is protected and requires a licence
  for use. The page publishes no copyright line and no MIT permission text, so there is no further
  notice text to reproduce.
- Used in: `shaders/library.glsl` (`cosinePalette`) and `examples/shader/visuals/spectrum-rings.glsl`
  (inline form).
