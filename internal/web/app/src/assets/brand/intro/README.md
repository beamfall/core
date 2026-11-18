# Vendored Cut Beam intros

Copies of `assets/brand/intro/beamfall-lockup-intro.svg` and `beamfall-stacked-intro.svg` from
beamfall-design-system (CORE-2555, vendored at a949db5). `brand-splash-plugin.ts` inlines both into
`index.html` as the cold-load splash (CORE-2556). Do not edit them here: rerun
`node scripts/vendor-brand-intro.mjs` against the design system. `tools/gate/design-token-drift.mjs`
fails when a copy differs from the sibling design-system checkout.
