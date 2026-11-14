# Tasks (CORE-1659)

- [x] 1. Read `serverInfo.playbackAdjustments`; send `adjustment` on the playback-info request
- [x] 2. Engine: restart at the same song position; progress and resume in song seconds
- [x] 3. Transport: time, duration, scrubber and seeks in song seconds under a tempo
- [x] 4. Key and tempo controls in the player bar, shown only with the capability for a karaoke
      song, clamped to the ranges, labelled, keyboard operable, with a live region
- [x] 5. Show "not applied" when the server did not echo the adjustment
- [x] 6. Component tests: shown, hidden (no capability, not karaoke), applying (request carries
      the adjustment, position conversion)
- [x] 7. Media session position and seeks, and auto-skip markers, in song seconds under a tempo
