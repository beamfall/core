## 1. Keyframe index

- [x] 1.1 `Runner.ProbeKeyframes` in `internal/ffmpeg/probe_keyframes.go`: one ffprobe packet scan of the first video stream, keyframe times relative to the container start
- [x] 1.2 `playback_keyframes` table (catalog migration 0114) and `Store.PlaybackKeyframes` / `Store.PutPlaybackKeyframes`, keyed by path and checked against size and modification time

## 2. Session timeline

- [x] 2.1 Copy-video sessions build the variant playlist from the keyframe index
- [x] 2.2 `Runner.StartHLSKeyframeSegments` seeks to and stamps the segment's exact keyframe time and cuts at every keyframe
- [x] 2.3 Segment arithmetic (source end, resume, reap, waits) uses the timeline

## 3. Tests

- [x] 3.1 7s-GOP fixture: declared durations equal the keyframe intervals and sum to the file duration within one frame
- [x] 3.2 Restart at segment N: first video PTS of seg N equals the playlist start of N (parsed fMP4)
- [x] 3.3 The probe runs once per file generation across sessions sharing a store
