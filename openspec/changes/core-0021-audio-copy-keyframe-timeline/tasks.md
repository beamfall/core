## 1. Keyframe index

- [ ] 1.1 `Runner.ProbeKeyframes` in `internal/ffmpeg/probe_keyframes.go`: one ffprobe packet scan of the first video stream, keyframe times relative to the container start
- [ ] 1.2 `playback_keyframes` table (catalog migration 0114) and `Store.PlaybackKeyframes` / `Store.PutPlaybackKeyframes`, keyed by path and checked against size and modification time

## 2. Session timeline

- [ ] 2.1 Copy-video sessions build the variant playlist from the keyframe index
- [ ] 2.2 `Runner.StartHLSKeyframeSegments` seeks to and stamps the segment's exact keyframe time and cuts at every keyframe
- [ ] 2.3 Segment arithmetic (source end, resume, reap, waits) uses the timeline

## 3. Tests

- [ ] 3.1 7s-GOP fixture: declared durations equal the keyframe intervals and sum to the file duration within one frame
- [ ] 3.2 Restart at segment N: first video PTS of seg N equals the playlist start of N (parsed fMP4)
- [ ] 3.3 The probe runs once per file generation across sessions sharing a store
