- [x] OpenSpec change with the design and spec delta (this change)
- [x] Store: per-title Media counts in the entity facet, cap keeps the most used titles
- [x] Graph: `counts` on `libraryItems.facets.groupings`; wire goldens regenerated; additive
      client-core-wire minor published
- [x] Web: featured artwork tiles with counts, long-tail top list, See all A-Z list with filter and
      letter row, jump links, hidden empty kinds
- [x] Web strings in every shipped locale; Go and vitest coverage
- [x] Screenshots at 1280 and 390 widths against a local fixture server (scratch, not committed)
- [ ] Verified on tower Films and TV libraries at desktop and phone widths (lander)
- [ ] `make gate` via `corvint-tasks gate run` (lander)
- [ ] `openspec archive core-2505-categories-redesign` after landing
