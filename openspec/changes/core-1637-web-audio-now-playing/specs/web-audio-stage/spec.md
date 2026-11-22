## ADDED Requirements

### Requirement: Now Playing links, Up Next and quality

The audio stage SHALL link the title to the item's page and each credit to its Entity page, SHALL
list the next items of the queue that drives playback without keeping a second queue, and SHALL
show the playback-info audio quality: codec, bit depth and sample rate when known, and direct play
or transcode.

#### Scenario: Album track from a queue

- **WHEN** a track plays from a queue of several tracks and playback-info reports its quality
- **THEN** the stage links the title, artist and album, lists the following tracks, and shows a
  badge such as "FLAC · 24-bit / 96 kHz · Direct play"

#### Scenario: Older server

- **WHEN** playback-info carries no audio quality
- **THEN** no badge is shown

### Requirement: Audio transport has shuffle and repeat and no video-only controls

On the audio stage the transport SHALL offer previous, next, shuffle and repeat (off, all, one),
SHALL NOT offer picture-in-picture or fullscreen, and SHALL NOT auto-hide. Shuffle and repeat SHALL
reorder or wrap the existing queue only, and the next item the player prepares SHALL follow them.

#### Scenario: Repeat all at the end of the queue

- **WHEN** repeat all is on and the last item of the queue ends
- **THEN** the first item plays

#### Scenario: Repeat one

- **WHEN** repeat one is on and the item ends
- **THEN** the same item plays again from the start

#### Scenario: Idle listener

- **WHEN** a track plays and the pointer stays still
- **THEN** the transport stays visible
