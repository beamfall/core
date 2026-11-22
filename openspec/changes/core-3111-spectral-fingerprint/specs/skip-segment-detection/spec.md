## ADDED Requirements

### Requirement: Spectral sibling fingerprint

When the detector offers a spectral fingerprint, skip detection SHALL compare a Media with its
ordered siblings by per-frame spectral hashes of the head (intro) and tail (credits) regions instead
of per-second loudness, searching every relative offset so a shared run MAY start at a different
time in each file. Without a spectral fingerprint the loudness comparison SHALL apply unchanged.

#### Scenario: Intro after a cold open

- **WHEN** three episodes share a theme from about 5 s to 31 s and differ in sound, but not in
  loudness, everywhere else
- **THEN** each episode gets a detected intro starting within 2 s of 5 s and ending within 2 s of
  31 s

#### Scenario: Shared run at different times

- **WHEN** the shared theme starts at 10 s in this Media and at 4 s in its sibling
- **THEN** the detected intro starts at 10 s in this Media

#### Scenario: Different audio does not match

- **WHEN** the head regions of two siblings share no audio
- **THEN** no intro is detected

#### Scenario: Identical files

- **WHEN** a sibling carries the same audio as this Media end to end
- **THEN** that sibling yields no intro, because no boundary was seen

### Requirement: Spectral shared run

A one-second window of this Media SHALL count as shared when its frames' mean Hamming distance to
the sibling's frames at that offset is at most 5 bits. The longest run of shared windows at any
offset SHALL become the candidate segment, subject to the existing intro cap, runtime-fraction cap,
whole-overlap rejection, minimum length and confidence threshold.

#### Scenario: Longest run wins

- **WHEN** two offsets each yield a shared run, of 20 s and of 26 s
- **THEN** the 26 s run is the candidate segment

### Requirement: Spectral fingerprint hash

The FFmpeg Runner SHALL produce the spectral fingerprint from 8 kHz mono PCM decoded through the
hardened spawn seam, one 32-bit hash per 0.125 s frame, each bit the sign of the difference between
adjacent log-spaced band energies with a relative floor. It SHALL NOT depend on Chromaprint or other
GPL-licensed code. Scaling the audio's gain SHALL NOT change the hash.

#### Scenario: Gain invariance

- **WHEN** the same audio is hashed at full and at half amplitude
- **THEN** the two hash sequences are identical
