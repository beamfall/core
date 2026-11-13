## 1. Server

- [x] 1.1 `Sessions`: wait in budget-long windows while the run the request waits on stays current, started at or before the segment, and writes a segment each window
- [x] 1.2 Tests: slow encoder blocks without a restart, cached segments past the encoder do not mask progress, a replaced run fails after one window, stalled encoder fails after one window, forward-seek latency is independent of distance

## 2. Web

- [x] 2.1 `usePlayer`: report the last presented position (not seeking, current frame decoded), nothing before the first frame
- [x] 2.2 Tests: stalled seek, no frame yet, a 503 leaves the playhead where it is
