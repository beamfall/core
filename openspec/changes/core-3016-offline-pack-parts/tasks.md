## 1. Core

- [x] 1.1 Migration 0129 adds `offline_pack_item.part_ordinal` and keys items by (pack_id, media_id, part_ordinal); backup fixtures regenerated
- [x] 1.2 Builder packs every Part of the selected Version in order and refuses a Version with a missing Part
- [x] 1.3 Manifest items carry optional `part_ordinal` and chapter `markers`; validation keys duplicates by Part
- [x] 1.4 Content route serves `/<media_id>/<part>`
- [x] 1.5 Test: stale offline audio progress is ignored by event time
- [x] 1.6 `manifestParts` opt-in: without it Markers are left out and multi-Part Media are refused (`parts_unsupported`); ADR-0157 Amendment A

## 2. Audiobook plugin

- [x] 2.1 `PackResolvers()` with kind `audiobook-book`; tagged release
- [x] 2.2 Core repins the plugin and registers the resolver; focused resolver test packs every Part and its Markers
