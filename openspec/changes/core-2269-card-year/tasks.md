- [x] `internal/store`: `SourceItem.Year`, filled by the per-card media read that sets `MediaClass`
- [x] `internal/graph`: `SourceItem.year`; semantic `sourceItems` cards carry year and class too
- [x] Tests: year present, key omitted without a year, Entity cards, restricted and locked unchanged
- [x] Wire contract 2.17.0 published and goldens regenerated with the added field
- [ ] `make gate`
- [ ] `openspec archive core-2269-card-year` after landing

## Not built: `positionLabel`

A card's position label ("Season 2 Episode 4") needs data Core does not hold. To ticket against
plugin-sdk before Core can render it:

- A display ordinal. `entity_relation.sort_order` orders items and is not a number to show: the
  film and TV local hydration stores month and day for a dated item and the air year for its
  container, and scraper hydration falls back to the document index when the kind maps no
  `OrdinalField`. No bundled plugin declares `OrdinalField` or `OrderingSourceField`.
- A localized phrase. A plugin declares a kind's `DisplayName` and a class's `MediaClassLabels`,
  but nothing that says how a label and a number combine in a locale.
