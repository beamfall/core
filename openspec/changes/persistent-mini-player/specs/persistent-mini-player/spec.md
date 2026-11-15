## ADDED Requirements

### Requirement: Audio outlives the watch route

When the viewer leaves the watch route while an audio Media is playing, the web viewer SHALL keep
playing it on the same media element, from the same position, on every other viewer route.

#### Scenario: Browse to search and back

- **WHEN** a track is playing and the listener goes to Browse, then to search
- **THEN** the track keeps playing and its position keeps advancing
- **AND** opening the full player again shows the same track, still playing, with no reload

### Requirement: Docked mini player

While an audio Media plays with no watch route open, the viewer SHALL show a docked mini player with
the artwork, title, credited people, a play/pause control, a next control, a progress bar and a
control that opens the full player. It SHALL be a labelled region of native buttons, reachable by
keyboard and announced by name.

#### Scenario: Controls

- **WHEN** the mini player is docked
- **THEN** play/pause, next, open and stop are focusable buttons with accessible names
- **AND** the progress bar exposes its position as text

#### Scenario: Advance while docked

- **WHEN** a track ends, or Next is chosen, while docked
- **THEN** the next track plays in the mini player and the page being browsed stays

#### Scenario: Stop

- **WHEN** the listener chooses Stop
- **THEN** playback ends and the mini player closes

### Requirement: Video keeps the full-screen route

A video Media SHALL play only on the watch route. Leaving the route SHALL stop it, with no mini
player.

#### Scenario: Leave a film

- **WHEN** a video Media is playing and the viewer goes back
- **THEN** playback stops and no mini player is shown

### Requirement: Restrictions still apply while docked

A relock SHALL pause playback and close the mini player (ADR-0004). A terminal failure, an advance
into a Media that is not audio, and a Profile switch SHALL end playback.

#### Scenario: Relock while docked

- **WHEN** the session relocks while the mini player is docked
- **THEN** playback pauses and the mini player closes
