# Tasks (CORE-1634)

- [x] 1. Decoded tracks on their own AudioContext; the media element is never routed through Web Audio
- [x] 2. Decode the next direct-play track 30 s before the end; trim the declared delay and padding only over near silence
- [x] 3. Schedule the join on the Web Audio clock; element plays on muted and in step (listener's mute kept apart); later tracks chained
- [x] 4. Hand sound back to the element on rate change, unplanned advance and unmount; mirror volume and mute
- [x] 5. Unit tests for the trim, the scheduling and the hand-back
- [x] 6. Playwright test measuring the silence and overlap between test tones at each join (Chromium, Firefox, WebKit), every track on the context
- [x] 7. Bound decoded audio: skip a next track over 12 minutes, release each finished track, hold at most two
- [x] 8. Start a decodable track that starts from silence on Web Audio (element held muted, 2 s start wait); keep it there across pause and seek
