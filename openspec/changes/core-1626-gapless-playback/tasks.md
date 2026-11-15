# Tasks (CORE-1626)

- [x] 1. Bounded ffprobe gapless pass (skip_samples / discard_padding, iTunSMPB override, decoded length)
- [x] 2. `media_file_stream` gapless columns (catalog 0118, encrypted-library 0006), scan mapping, backup fixture corpus regenerated
- [x] 3. Live encodes of picture-less sources keep the AAC priming (no `-ss 0`)
- [x] 4. `streams[].gapless` on playbackInfo for direct play and live AAC transcodes; wire goldens regenerated
- [x] 5. Real-encode tests: probe values for MP3/AAC/Opus; two transcoded pieces join without a gap
- [ ] 6. Publish the wire contract version carrying `streams[].gapless` (batch lead)
- [ ] 7. Client players trim by `gapless` (client repositories)
- [ ] 8. Follow-up: gapless for resumed/restarted transcodes and audio-copy streams
