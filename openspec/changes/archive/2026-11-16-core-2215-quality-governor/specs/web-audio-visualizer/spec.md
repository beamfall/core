## ADDED Requirements

### Requirement: The visualizer scales render size and detail with the Oscillux quality governor

The Web Viewer visualizer SHALL drive its visual render size and detail reduction from the
Oscillux quality governor in the pinned wasm core, using the reference web host's quality ladder,
so that frame pacing holds on displays the GPU cannot fill at full size. Core SHALL NOT implement
its own quality heuristics. The present pass and the frame flash limiter SHALL keep running at
the full canvas size.

#### Scenario: Slow frames

- **WHEN** frame intervals stay above the governor's target
- **THEN** the governor demotes the level and the visual renders at the smaller scale with more
  detail reduction, while the canvas keeps its size

#### Scenario: Headroom returns

- **WHEN** frame intervals stay well under the target
- **THEN** the governor promotes the level back toward full size and full detail

#### Scenario: Switch running

- **WHEN** a switch between visuals is running
- **THEN** its frames are not fed to the governor and the level does not change until it ends
