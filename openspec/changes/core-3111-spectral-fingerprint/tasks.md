- [x] FFmpeg: `SpectralFingerprint` / `SpectralFingerprintTail` decode bounded PCM and hash 0.125 s
      frames (pure-Go FFT, band-difference bits, -50 dB floor)
- [x] FFmpeg: unit tests for the hash (gain invariance, distinct tones differ) and an I/O journey test
- [x] Skip detection: optional spectral capability, offset-searching run matcher, loudness fallback
- [x] Skip detection: tests for cold-open intro, offset intro, no match, identical files, CORE-3079
      regressions still green
- [x] Verify on the synthetic Intro Show corpus: E01-E03 intro within 2 s of 5-31 s
