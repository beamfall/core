- [x] Graph: `mediaTracks(mediaID)` session-role read with versions, audio tracks and caption tracks
- [x] Wire goldens regenerated with `make regen-wire-goldens`; client-core-wire minor published
- [x] Web: Version, Audio and Subtitles pickers on the Media detail page
- [x] Web: audio and subtitle picks reach playback-info through the player's start tracks
- [x] Web: the picked Version rides the navigation state; Auto Version choice; picks apply on every
      start from the page without overwriting later in-player picks
- [x] Web strings in every shipped locale (existing player keys reused); Go and vitest coverage
- [ ] Verified on tower at desktop and 375px widths (lander)
- [x] `make gate` (gate run plexpar-gate-1118g PASSED at 153215ed)
- [ ] `openspec archive core-2442-detail-track-pickers` after landing
