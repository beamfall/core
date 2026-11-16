# Tasks (CORE-1634)

- [x] 1. One shared Web Audio graph per media element (source, element gain, optional delay); audio delay uses it
- [x] 2. Decode the next direct-play track 30 s before the end; trim the declared delay and padding only over near silence
- [x] 3. Schedule the join on the Web Audio clock; element plays on silently and in step; later tracks chained
- [x] 4. Hand sound back to the element on pause, seek, rate change, unplanned advance and unmount; mirror volume and mute
- [x] 5. Unit tests for the trim, the scheduling and the hand-back
- [x] 6. Playwright test measuring the silence between test tones at each join (Chromium)
- [ ] 7. Follow-up: Safari (native HLS engine) and HLS next tracks
