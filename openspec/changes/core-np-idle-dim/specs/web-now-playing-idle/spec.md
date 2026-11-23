## ADDED Requirements

### Requirement: Idle music Now Playing recedes

While audio plays on the web full-screen Now Playing stage and the listener gives no input for 3
seconds, the web client SHALL dim the transport, the top bar and the artwork to translucent, and
SHALL restore them on any input. It SHALL NOT dim while paused, while a menu or popover is open,
or while the pointer or keyboard focus is on a control.

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
