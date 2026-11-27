## 1. FFmpeg and App Adapter

- [x] 1.1 Extend `-progress pipe:1` parsing for `out_time_us` + `speed` and add `Runner.ConvertWithProgress`
- [x] 1.2 Forward live `ConvertProgress` (and probed duration fallback) from `offlinePackTranscoder`

## 2. Offline Pack Service and Graph Wire

- [x] 2.1 Stream monotonic throttled transcode progress and live `transcodeSpeed` in `offlinepack`
- [x] 2.2 Expose `transcodeSpeed` on `OfflinePackInfo` and regenerate wire contract goldens
- [x] 2.3 Verify with `gofmt`, focused `-race` tests, `golangci-lint`, `openspec validate`, and Codex review
