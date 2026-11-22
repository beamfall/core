## 1. Core

- [ ] 1.1 Migration 0129 adds `offline_pack_item.part_ordinal` and keys items by (pack_id, media_id, part_ordinal); backup fixtures regenerated
- [ ] 1.2 Builder packs every Part of the selected Version in order and refuses a Version with a missing Part
- [ ] 1.3 Manifest items carry optional `part_ordinal` and chapter `markers`; validation keys duplicates by Part
- [ ] 1.4 Content route serves `/<media_id>/<part>`
- [ ] 1.5 Test: stale offline audio progress is ignored by event time

## 2. Audiobook plugin

- [ ] 2.1 `PackResolvers()` with kind `audiobook-book`; tagged release
- [ ] 2.2 Core repins the plugin and registers the resolver; focused resolver test packs every Part and its Markers
