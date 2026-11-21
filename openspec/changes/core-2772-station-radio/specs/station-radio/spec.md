## ADDED Requirements

### Requirement: Catalog station rules

Station generation SHALL apply genre, mood, and decade alongside existing hard
rules before ranking. Genre and mood match catalog values without case
sensitivity. A decade is a positive multiple of ten and includes its first
year through its ninth following year.

#### Scenario: Filter the window

- **WHEN** a client starts or previews a station with genre, mood, or decade rules
- **THEN** only visible candidates matching all requested rules enter the window

### Requirement: Start radio from the viewer

The served viewer SHALL offer Start radio on audio track, artist, and album
detail pages and their menus. It SHALL require confirmation before replacing a
non-empty visible queue and start playback from the first playable result.

#### Scenario: Replace the visible queue

- **WHEN** the viewer confirms replacement
- **THEN** the station replaces the visible queue and plays its first playable entry
- **AND** the queue shows a localized reason on each generated entry after a reload
