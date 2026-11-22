## 1. Server

- [x] 1.1 Add `Mosaic` to `EntityPosterRule`; compose four distinct source posters into a 2x2 JPEG inherited poster
- [x] 1.2 Fall back to a single inherited poster with fewer than four distinct posters or an undecodable tile
- [x] 1.3 Record tile sources in the inherited marker; skip unchanged mosaics and recompose on a changed tile
- [x] 1.4 Derive mosaic rules for undeclared category-archetype kinds outside conformance mode

## 2. Tests

- [x] 2.1 Artwork test: mosaic layout, tile dedupe, idempotent second sweep, refresh on a changed source, single-poster fallback, own poster kept
- [x] 2.2 App test: derived rule sources, a declared rule suppresses the derived one
