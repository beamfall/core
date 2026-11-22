- [x] `internal/store`: read each linked Entity's class span once per request
- [x] `internal/graph`: `mediaForEntity(rollUp:)` argument, container roll-up, schema argument
- [x] Graph tests and a benchmark over a nested-container fixture; wire goldens regenerated with
  `make regen-wire-goldens`
- [x] Web: request the rolled-up list, count and play through rolled-up containers
- [x] Web: watch actions only on container-archetype Entity pages; vitest coverage
- [ ] Wire contract published as a new version (lander)
- [ ] `make gate`
- [ ] `openspec archive core-3058-entity-page-rollup` after landing
