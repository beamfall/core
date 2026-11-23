# web-waveform-mini Specification

## Purpose
The docked mini player shows the playing track's waveform as its progress strip, reusing the full player's peaks.

## Requirements

### Requirement: Mini player waveform strip

The docked mini player SHALL draw the playing audio track's waveform in its progress strip, with
played and unplayed peaks styled differently, using the peaks the web player already cached for that
Media. While peaks load it SHALL keep the waveform shape; when they are unavailable it SHALL show the
plain strip. The strip SHALL be a labelled `slider`: clicking or dragging it SHALL seek playback to
that position, and the arrow, Page Up, Page Down, Home and End keys SHALL seek from the keyboard.

#### Scenario: Docking with cached peaks

- **WHEN** the listener docks the player while an audio track whose peaks were loaded is playing
- **THEN** the mini player's strip shows those peaks without a new request

#### Scenario: Peaks unavailable

- **WHEN** the waveform request fails
- **THEN** the mini player shows the plain progress strip

#### Scenario: Advancing while docked

- **WHEN** playback advances while docked to a next track whose peaks were prefetched
- **THEN** the strip shows the new peaks without falling back to the plain strip

#### Scenario: Seeking from the strip

- **WHEN** the listener clicks three quarters of the way along the strip of a 30-second track
- **THEN** playback moves to about 22 seconds

### Requirement: Mini player Next names the next track

When the queue has a next item, the mini player's Next control SHALL show its title on hover and
keyboard focus and SHALL include it in its accessible name.

#### Scenario: Hovering Next in the mini player

- **WHEN** the next item is titled "Track Two" and the listener hovers Next in the mini player
- **THEN** a label reading "Next" and "Track Two" appears, and the control's accessible name is "Next: Track Two"
