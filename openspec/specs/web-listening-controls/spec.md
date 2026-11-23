# web-listening-controls Specification

## Purpose
Defines how the web client's listening controls, such as the sleep timer, end playback gently and give way to the listener.

## Requirements

### Requirement: Sleep timer fades gradually

The web sleep timer SHALL fade the volume out with an equal-power curve over its final 45 seconds,
or over a quarter of a timer set with less than 3 minutes left, then pause and restore the
listener's volume. The fade SHALL NOT be shown or saved as the listener's volume.

#### Scenario: Minutes timer

- **WHEN** a 15-minute timer has 22.5 seconds left
- **THEN** the volume is about 71% of the listener's level, and at the stop playback pauses with
  the listener's level back

### Requirement: Sleep fade yields to the listener

Cancelling or extending the sleep timer during its fade SHALL restore the volume at once. A volume
the listener sets, a pause, a play or a skip during the fade SHALL end that fade without changing
the listener's level, and the time left SHALL fade afresh from that level.

#### Scenario: Timer cancelled mid-fade

- **WHEN** the listener turns the timer off during the fade
- **THEN** the volume returns to the listener's level at once and playback continues

#### Scenario: Listener sets the volume mid-fade

- **WHEN** the listener sets the volume to 50% with 20 seconds left
- **THEN** the volume stays at 50%, fades from there over the 20 seconds, and is 50% again after
  the pause
