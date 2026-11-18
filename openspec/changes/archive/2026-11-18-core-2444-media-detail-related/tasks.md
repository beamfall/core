- [x] Web: "More with <first billed person>" row on the Media detail page, current item excluded
- [x] Web: Related row ranked by shared category credits and billed people, current item excluded
- [x] Graph: `relatedMedia(mediaID, limit)` ranked server-side through the `entity-media` Source
      (Mute, visibility, locked), additive, in client-core-wire 2.26.0 with goldens regenerated
- [x] Web: Related row from `relatedMedia`; More with from the bounded `entity-media` Source; the
      lead is the first person credit in server order
- [x] Web strings in every shipped locale; vitest coverage for both rows, the empty case and a
      failed read
- [x] Verified on tower at desktop and 375px widths (lander; 716f3bd3 on the tower, 2026-11-18)
- [x] `make gate` (gate run plexpar-gate-1118g PASSED at 153215ed)
- [x] `openspec archive core-2444-media-detail-related` after landing
