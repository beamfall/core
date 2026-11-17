- [x] `ffmpeg.Runner.ExtractSubtitlesToWebVTT`: several text streams in one run, with a test over a
      two-track MKV
- [x] Per-file single-flight, extraction slots and bounded wait in the subtitle route
- [x] `503` + `Retry-After` for a pending extraction on the plaintext and encrypted paths
- [x] Per-key lock for encrypted subtitle extraction
- [x] Tests: same-file single conversion, no cross-file serialization, bounded wait, Retry-After,
      one-pass extraction of every text track
