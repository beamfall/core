# Tasks (CORE-1627)

- [x] 1. Record ffprobe `sample_rate` for every audio stream (bounded); bit depth already recorded
- [x] 2. `audioCodecs[].maxSampleRate` / `maxBitDepth` on the capability profile
- [x] 3. Direct play and passthrough refuse a source above either ceiling; unrecorded values are not refused
- [x] 4. `audioQuality` and `next.audioQuality` on playbackInfo; wire goldens regenerated
- [x] 5. Publish wire contract 2.11.0 (registry active version unchanged)
- [ ] 6. Re-vendor 2.11.0 in apple-ui, android, android-ui and web, then pin it active in the registry
- [ ] 7. Show the audio quality in the clients (web: CORE-1636)
