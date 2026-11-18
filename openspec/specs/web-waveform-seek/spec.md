# web-waveform-seek Specification

## Purpose
Music playback in the web player shows the track's waveform as the seek bar, generated and cached by the server for every audio Media.

## Requirements

### Requirement: Audio decisions advertise a waveform

The playback decision for an audio-class Media SHALL include `assets.waveform` even when the
waveform envelope has not been generated yet. Decisions for other Media classes SHALL include it
only when the envelope exists.

#### Scenario: Audio track before the sweep

- **WHEN** a client asks for a playback decision for an audio Media whose waveform is not generated
- **THEN** the decision includes an `assets.waveform` URL

#### Scenario: Video without a waveform

- **WHEN** a client asks for a playback decision for a video Media with no waveform envelope
- **THEN** the decision has no `assets.waveform` URL

### Requirement: Waveform generated on demand

The stream-token asset route SHALL generate a missing waveform envelope when it is requested, with
at most one generation per content fingerprint at a time and a bounded number of concurrent
generations, and SHALL serve the result. When generation fails it SHALL answer 404.

#### Scenario: First request generates

- **WHEN** a client fetches the waveform URL of an audio Media that has no envelope yet
- **THEN** the server generates the envelope and serves it in the same response

#### Scenario: Generation fails

- **WHEN** generation of a requested waveform fails
- **THEN** the route answers 404

### Requirement: Web player waveform seek bar

The web player SHALL draw an audio track's waveform as its seek bar, with played and unplayed peaks
styled differently, seeking on click or drag. The bar SHALL keep the slider role and keyboard
seeking. While peaks load or when they are unavailable it SHALL show the plain bar. Moving to the
next track SHALL NOT unmount the bar, and the next track's peaks SHALL be fetched ahead of time.

#### Scenario: Peaks available

- **WHEN** an audio track plays and its waveform loads
- **THEN** the seek bar shows the peaks, with the played part styled apart from the rest

#### Scenario: Peaks unavailable

- **WHEN** the waveform request fails
- **THEN** the seek bar is the plain progress bar and seeking still works

#### Scenario: Next song

- **WHEN** playback advances to the next queued track whose peaks were prefetched
- **THEN** the seek bar shows the new peaks without falling back to the plain bar
