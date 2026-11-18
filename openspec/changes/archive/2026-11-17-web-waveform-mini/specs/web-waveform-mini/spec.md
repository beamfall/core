## ADDED Requirements

### Requirement: Mini player waveform strip

The docked mini player SHALL draw the playing audio track's waveform in its progress strip, with
played and unplayed peaks styled differently, using the peaks the web player already cached for that
Media. While peaks load it SHALL keep the waveform shape; when they are unavailable it SHALL show the
plain strip. The strip SHALL keep its `progressbar` role and label.

#### Scenario: Docking with cached peaks

- **WHEN** the listener docks the player while an audio track whose peaks were loaded is playing
- **THEN** the mini player's strip shows those peaks without a new request

#### Scenario: Peaks unavailable

- **WHEN** the waveform request fails
- **THEN** the mini player shows the plain progress strip

#### Scenario: Advancing while docked

- **WHEN** playback advances while docked to a next track whose peaks were prefetched
- **THEN** the strip shows the new peaks without falling back to the plain strip
