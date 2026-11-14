## ADDED Requirements

### Requirement: Chapters are the Markers without a segment kind

A player SHALL treat every Marker whose segment kind is empty as a chapter, whether it is a point
or a range, and SHALL NOT list intro, recap or credits segments as chapters. Chapters SHALL be
ordered by start time, one per distinct start time. A chapter without a title SHALL be labelled
with its position or its start time.

#### Scenario: Container chapters arrive as ranges

- **WHEN** a Media item has three imported container chapters with end times and one intro segment
- **THEN** the chapter list shows the three chapters in start order and not the intro

### Requirement: Players open a chapter list that seeks on select

The web, Android TV, iPhone and macOS players SHALL offer a Chapters control while the playing
item has at least one chapter. It SHALL open a list showing each chapter's title and start time
and marking the current chapter. Choosing a chapter SHALL seek to its start and close the list.
The control SHALL be absent when the item has no chapters.

#### Scenario: Jump to a chapter

- **WHEN** a viewer opens the chapter list and chooses the third chapter
- **THEN** playback seeks to the third chapter's start and the list closes

### Requirement: Chapter thumbnails show when the server has them

Each chapter row SHALL show the Marker thumbnail at `assets.markerThumbnailBase` followed by
`marker-<start in whole milliseconds>.jpg` when that image loads. When it does not load, the web
player SHALL show the trickplay tile for the chapter start if a trickplay manifest is loaded, and
otherwise no image. A missing image SHALL never show an error.

#### Scenario: Thumbnail not generated yet

- **WHEN** a chapter's Marker thumbnail returns 404 and no trickplay manifest exists
- **THEN** the row shows the title and time without an image or error
