- [x] Store: `SimilarMedia` read (seed centroid, seed exclusion, scoping, limit and scan budget)
- [x] Store: vector scan keeps a real cosine of -1; stable O(n log n) candidate sort
- [x] Fixture `testdata/similar_media_fixture.json` and store tests; reveal-contract test no longer
      passes vacuously
- [x] Design, Oscillux API note, owner decisions and ticket map (`design.md`)
- [x] Owner decisions D1-D5 accepted as recommended (CORE-3070)
- [x] Per-Library sonic-analysis toggle, default OFF, Admin read and mutation (CORE-3073)
- [x] Idle-only excerpt analysis job behind `SonicAnalyzer`, FFmpeg `AudioExcerpt` (CORE-3073)
- [x] `similarMedia` graph read and `sonic` Station seed kind (CORE-3074)
- [x] Web "Sonically similar" row on audio Media detail (CORE-3075)
- [ ] Publish the client-core-wire minor version (landing owner)
- [ ] Oscillux sonic profile release and the wazero loader over its WASI artifact (CORE-3071,
      CORE-3072)
- [ ] Console toggle and "Start sonic mix" entry point (follow-up tickets)
