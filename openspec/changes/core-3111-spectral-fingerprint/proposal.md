# Skip detection: spectral fingerprint with offset search

Ticket: CORE-3111 (`queue:core:main`).

## Why

Sibling fingerprinting compares one loudness value per second, anchored at the first second of each
file. Two episodes whose content has the same loudness curve, but different sound, look identical
end to end, and an intro that starts after a cold open cannot be found at all. On the synthetic
Intro Show corpus (three 111 s episodes, a shared theme from about 5 s to 31 s, and shared closing
music) the loudness signature matches every window. Since CORE-3079 that whole-file run is correctly
rejected, so no intro is detected, while Jellyfin's Chromaprint-based detector finds 5-31 s.

## What changes

- The FFmpeg Runner gains `SpectralFingerprint` and `SpectralFingerprintTail`. FFmpeg decodes the
  bounded head or tail region of the primary audio stream to 8 kHz mono PCM on stdout through the
  existing hardened spawn seam. Go code hashes it into one 32-bit sub-fingerprint per 0.125 s frame:
  each bit is the sign of the energy difference between two adjacent log-spaced bands (250-3500 Hz)
  with a floor at -50 dB below the frame's loudest band, so the hash is gain-invariant and empty
  bands compare equal. The hash is written here; no Chromaprint or other GPL code is added.
- Skip detection uses the spectral fingerprint when the detector offers it and keeps the loudness
  fingerprint as the fallback (and for recap detection). Spectral matching searches every relative
  offset between the two files at frame resolution, groups this Media's frames into one-second
  windows. Each frame pair agrees, disagrees or carries no evidence (shared silence, or a held tone
  or hum on both sides); a window is shared when at least two pairs agree and at most a quarter
  disagree, and differs when at least two pairs disagree and they outnumber the agreeing ones. The
  best run of shared windows at an offset where the files also differ becomes the candidate intro
  (head) or credits (tail), so an intro after a cold open and credits that end before a
  post-credits scene are found.
- A sibling that never differs from this Media at some offset, yet shares at least 15 s there
  outside the candidate, is the same recording (perhaps padded with silence) and yields nothing.
- The CORE-3079 guards stay: a run capped at 120 s for intros, at half the runtime, at least 15 s
  long and confident enough to clear the 0.80 auto-apply threshold.

No wire, schema or migration change.

## Non-goals

- Recap (reverse-fingerprint) detection keeps the loudness signature.
- Sub-second segment boundaries: segments stay on whole-second windows.
