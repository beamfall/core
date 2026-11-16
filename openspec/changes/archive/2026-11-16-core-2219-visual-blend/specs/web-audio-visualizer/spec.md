## ADDED Requirements

### Requirement: The visualizer blends between visuals with the Oscillux switch pass

When the Web Viewer visualizer changes from one Oscillux visual to another, it SHALL blend the
outgoing visual into the incoming one through the switch pass vendored unchanged from the pinned
Oscillux release, with the progress timed by the Oscillux transition scheduler, instead of
replacing the visual in one frame. Core SHALL NOT supply its own transition shader. Under
`prefers-reduced-motion` the visual SHALL change at once.

#### Scenario: Album change

- **WHEN** playback advances to an item whose artwork selects a different visual
- **THEN** both visuals draw while the switch pass blends from the outgoing one to the incoming
  one, and the outgoing visual is dropped once the switch ends

#### Scenario: Same artwork

- **WHEN** playback advances to an item that selects the same visual
- **THEN** the visual keeps drawing with no switch

#### Scenario: Reduced motion

- **WHEN** the visual changes while the user prefers reduced motion
- **THEN** the incoming visual replaces the outgoing one at once

#### Scenario: Change during a switch

- **WHEN** another visual is requested while a switch is running
- **THEN** it starts once the running switch ends
