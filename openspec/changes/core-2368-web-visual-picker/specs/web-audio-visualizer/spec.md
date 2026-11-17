## ADDED Requirements

### Requirement: The audio player offers every pinned Oscillux web Visual and lets the viewer choose

The Web Viewer SHALL offer every Visual whose web leg the pinned Oscillux release ships. Audio
playback settings SHALL include a Visualizer choice of Rotate, One per album, or any single Visual
by its Oscillux name, kept per browser and defaulting to Rotate.

#### Scenario: Pick a Visual

- **WHEN** the viewer picks a single Visual
- **THEN** that Visual draws for every track until the choice changes, and the choice survives a
  reload in the same browser

#### Scenario: One per album

- **WHEN** the choice is One per album
- **THEN** tracks with the same artwork show the same Visual

#### Scenario: Stale stored choice

- **WHEN** the stored choice names a Visual the pinned release does not have
- **THEN** the choice reads as Rotate

### Requirement: Rotate changes Visual per track and on a downbeat

When the Visualizer choice is Rotate, the Web Viewer SHALL show a different Visual for each track
and SHALL change Visual on a downbeat after a fixed span of drawn time. Under
`prefers-reduced-motion` it SHALL pick only Visuals Oscillux marks calm and SHALL NOT change Visual
on a timer.

#### Scenario: Rotate

- **WHEN** the choice is Rotate and the next track starts, or the span of drawn time passes and a
  downbeat arrives
- **THEN** the visualizer switches to a different Visual

#### Scenario: Reduced motion

- **WHEN** the choice is Rotate and the viewer prefers reduced motion
- **THEN** only calm Visuals are picked and no timed change happens

### Requirement: The quality governor sees the real frame interval

The Web Viewer visualizer SHALL record the unclamped interval between drawn frames into the
Oscillux quality governor, so the governor's own pause threshold, not the render step's clamp,
decides whether an interval was a pause or a slow frame.

#### Scenario: Tab paused

- **WHEN** a frame follows a long pause, such as a hidden tab
- **THEN** the governor ignores that interval and the render scale does not drop
