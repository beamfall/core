- [ ] FFmpeg: `SpectralFingerprint` / `SpectralFingerprintTail` decode bounded PCM and hash 0.125 s
      frames (pure-Go FFT, band-difference bits, -50 dB floor)
- [ ] FFmpeg: unit tests for the hash (gain invariance, distinct tones differ) and an I/O journey test
- [ ] Skip detection: optional spectral capability, offset-searching run matcher, loudness fallback
- [ ] Skip detection: tests for cold-open intro, offset intro, no match, identical files, CORE-3079
      regressions still green
- [ ] Verify on the synthetic Intro Show corpus: E01-E03 intro within 2 s of 5-31 s
