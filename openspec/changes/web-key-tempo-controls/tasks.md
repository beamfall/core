# Tasks (CORE-1659)

- [ ] 1. Read `serverInfo.playbackAdjustments`; send `adjustment` on the playback-info request
- [ ] 2. Engine: restart at the same song position; progress and resume in song seconds
- [ ] 3. Transport: time, duration, scrubber and seeks in song seconds under a tempo
- [ ] 4. Key and tempo controls in the player bar, shown only with the capability for a karaoke
      song, clamped to the ranges, labelled, keyboard operable, with a live region
- [ ] 5. Show "not applied" when the server did not echo the adjustment
- [ ] 6. Component tests: shown, hidden (no capability, not karaoke), applying (request carries
      the adjustment, position conversion)
