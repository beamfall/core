# web-music-play-queue Specification

## Purpose
The web audio player shows, reorders and edits the server Play Queue, so music playback in the browser follows the same queue every other client sees.

## Requirements

### Requirement: The web audio player exposes the server Play Queue

Opening the Play Queue panel SHALL read every visible page in server order without editing the
queue. The panel SHALL allow the viewer to start at the first playable audio Media.

#### Scenario: The queue spans pages

- **WHEN** a viewer opens the panel for a queue spanning multiple pages
- **THEN** the panel lists the visible rows in server order and makes only read requests

### Requirement: Queue navigation uses the route and the live Profile

Next, Previous and automatic advancement SHALL use the current route Media ID and the active
Profile's server queue. Only playable audio Media SHALL be candidates. A gapless handoff SHALL
use the same next Media as automatic advancement. Titles SHALL be read under the live identity.

#### Scenario: Reload midway through the queue

- **WHEN** the viewer reloads the watch route for B in a queue ordered A, B, C
- **THEN** Next and automatic advancement target C and Previous targets A

#### Scenario: The current row disappears

- **WHEN** the current Media is absent when the server queue is read for advancement
- **THEN** playback stops after that Media

#### Scenario: Access changes

- **WHEN** the viewer relocks or switches Profiles
- **THEN** titles read under the retired identity disappear
- **AND** a different Profile cannot continue the run
