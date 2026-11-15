# Tasks (CORE-1709)

- [x] 1. Server: `audioTracks` in playbackInfo, `selectedAudioIndex` in the request, decide ladder
      and stream-token grant, ffmpeg audio mapping; Go tests
- [x] 2. Wire contract goldens regenerated
- [x] 3. Web: file-sourced Audio and Subtitles menus, `<track>` captions, burn-in renegotiation,
      unavailable notice row; strings in every shipped locale
- [x] 4. Web unit tests and `e2e/player-tracks.spec.ts` (multi-audio, multi-subtitle file on HLS)
- [ ] 5. Publish wire contract 2.8.0; `make gate`; archive this change after landing
