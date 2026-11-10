## 1. Player start

- [x] 1.1 Attach the stream before markers and trickplay resolve
- [x] 1.2 Pass the resume position to hls.js and drop the hls.js metadata seek
- [x] 1.3 Keep the stream-token refresh at the current position
- [x] 1.4 Tests for the hung-markers, resume and refresh cases

## 2. Prefetch

- [x] 2.1 Prefetch the player chunk on media card pointer entry and focus
- [x] 2.2 Test the prefetch and the non-media case

## 3. Verify and close

- [x] 3.1 Run `make gate`
- [x] 3.2 Run `openspec archive web-player-start` after landing, before completing CORE-0965
