## 1. Server

- [x] 1.1 `Sessions`: wait in budget-long windows while the same encoder run keeps writing segments
- [x] 1.2 Tests: slow encoder blocks without a restart, stalled encoder fails after one window, forward-seek latency is independent of distance

## 2. Web

- [x] 2.1 `usePlayer`: report the last presented position, nothing before the first frame
- [x] 2.2 Tests: stalled seek, no frame yet, a 503 leaves the playhead where it is
