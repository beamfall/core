## ADDED Requirements

### Requirement: Web play queue stays within the playing item's class

The web player SHALL narrow its play queue to the class of the playing Media: an entry whose known
class differs SHALL be dropped, an entry with no known class SHALL stay, and the playing item SHALL
always stay. Queue position, previous/next, auto-advance and the queue handed to the next item SHALL
use the narrowed queue.

#### Scenario: Search results mixing tracks and videos

- **WHEN** a viewer plays a track from search results that list two tracks and a video
- **THEN** the queue position reads "1 of 2" and next plays the other track

#### Scenario: Declared next item is a video

- **WHEN** a track plays from a Home shelf and playback-info reports the next queue entry as video
- **THEN** that entry leaves the queue and next plays the following entry

#### Scenario: Unknown class

- **WHEN** no class is known for a queue entry
- **THEN** the entry stays in the queue

### Requirement: Web play queue learns Media classes

The web player SHALL take a queue entry's class from the row that built the queue when it reports
one, and from playback-info: the playing item's `mediaClass` and the declared next item's
`next.mediaClass`.

#### Scenario: Search row reports its class

- **WHEN** a viewer plays an item from search results
- **THEN** each queue entry carries the class its search row reported

### Requirement: Audio Media opens the audio stage while loading

When the playing item's queue entry reports class `audio`, the web player SHALL show the audio
stage, not an empty video frame, before playback-info answers.

#### Scenario: Track played from search

- **WHEN** a viewer plays a track from search results and playback-info has not answered yet
- **THEN** the audio stage shows the track's title

### Requirement: Audio stage reserves the visualizer slot

The audio stage SHALL render an empty slot where a visualizer from the pinned Oscillux release will
mount. Core SHALL NOT render visual effects into it.

#### Scenario: Audio playback

- **WHEN** an audio item plays
- **THEN** the audio stage contains the visualizer slot and the slot is empty
