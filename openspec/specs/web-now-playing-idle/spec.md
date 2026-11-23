# web-now-playing-idle Specification

## Purpose
Keeps the web full-screen Now Playing stage calm while music plays untouched, without losing controls the listener reaches for.

## Requirements

### Requirement: Idle music Now Playing recedes

While music plays on the web full-screen Now Playing stage and the listener gives no input for 3
seconds, the web client SHALL dim the transport, top bar and artwork, and SHALL restore them on
any input. It SHALL NOT dim while paused, while a menu, popover or the Up Next drawer is open, or
while keyboard focus or a hover-capable pointer is on a control.

#### Scenario: Idle while playing

- **WHEN** a track plays and the listener has not touched the page for 3 seconds
- **THEN** the controls, waveform and top bar are mostly transparent and the artwork is dimmed

#### Scenario: Activity

- **WHEN** the pointer moves or a key is pressed while the stage is dimmed
- **THEN** the controls and artwork fade back to full opacity

#### Scenario: Paused

- **WHEN** playback is paused
- **THEN** nothing is dimmed, however long the listener is idle

#### Scenario: Reduced motion

- **WHEN** prefers-reduced-motion is set and the stage goes idle
- **THEN** the opacity changes without an animated transition

### Requirement: Dimmed controls ignore the first tap on touch

On touch input a dimmed control on the music Now Playing stage SHALL ignore taps until the stage
wakes, and a control shown at full opacity SHALL accept them.

#### Scenario: Touch

- **WHEN** the listener taps a control on a touch screen and then leaves the page alone
- **THEN** the stage dims after 3 seconds, and the first tap on the dimmed stage wakes it without
  activating a control
