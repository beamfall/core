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

Each frame pair SHALL agree, disagree or carry no evidence; shared silence and a held tone or hum
on both sides carry none. A one-second window SHALL count as shared when at least two pairs agree
and at most a quarter disagree, and as differing when at least two pairs disagree and outnumber the
agreeing ones. The best run of shared windows at an offset with a differing window SHALL become the
candidate segment, under the existing segment guards.

#### Scenario: Held hum is no evidence

- **WHEN** after a shared intro both files hold the same hum while their other frames differ
- **THEN** the intro ends where the shared intro ends

#### Scenario: Held chord is evidence

- **WHEN** a shared intro holds the same chord for 3 s
- **THEN** the intro spans the chord

#### Scenario: Longest run wins

- **WHEN** two offsets each yield a shared run, of 20 s and of 26 s
- **THEN** the 26 s run is the candidate segment

### Requirement: Spectral duplicate rejection

When an offset with no differing window shares at least 15 windows that lie outside the candidate
segment on both files' timelines, the sibling SHALL yield no segment, because the two files are one
recording.

#### Scenario: Silence-padded copy

- **WHEN** a sibling is this Media's audio after 100 s of leading silence
- **THEN** that sibling yields no intro

#### Scenario: Theme reprise

- **WHEN** a sibling replays the shared theme later in its head region
- **THEN** the intro is still detected in both files

### Requirement: Spectral fingerprint hash

The FFmpeg Runner SHALL produce the spectral fingerprint from 8 kHz mono PCM decoded through the
hardened spawn seam, one 32-bit hash per 0.125 s frame, each bit the sign of the difference between
adjacent log-spaced band energies with a relative floor. It SHALL NOT depend on Chromaprint or other
GPL-licensed code. Scaling the audio's gain SHALL NOT change the hash.

#### Scenario: Gain invariance

- **WHEN** the same audio is hashed at full and at half amplitude
- **THEN** the two hash sequences are identical
