# Tasks (CORE-1658)

- [x] 1. Request `adjustment` with range validation; response echo; grant fields
- [x] 2. FFmpeg pitch and tempo filter builders (asetrate/atempo)
- [x] 3. Forced transcode: live composed picture or an audio-only HLS rung
- [x] 4. Sessions: duration divided by tempo, segment N at song position N x segment x tempo,
      composed picture retimed with the audio
- [x] 5. `serverInfo.playbackAdjustments`
- [x] 6. Tests: validation, filter arguments, picture retiming, restart at the same song position
- [x] 7. Wire goldens regenerated and wire contract published; the golden corpus stays
      byte-identical, since the default adjustment changes nothing
