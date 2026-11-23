# web-mini-player-links Specification

## Purpose
Lets the listener reach the playing item's artist and album straight from the docked web mini player.

## Requirements

### Requirement: Mini player links to the artist and album

The docked mini player SHALL link each credited person in its artist line to that person's Entity
page, and SHALL name the playing item's first container credit (its album) on the same line, linked
to that container's Entity page. When the album has an Entity, the artwork SHALL link to the same
page and SHALL stay out of the tab order. A credit without an Entity SHALL show as plain text.
Following a link SHALL navigate within the app and SHALL NOT interrupt playback.

#### Scenario: A track with two artists

- **WHEN** the mini player plays a track credited to two artists on an album
- **THEN** each artist's name links to their page and the album's name and the artwork link to the album's page

#### Scenario: A credit names no Entity

- **WHEN** a credit has no Entity ID
- **THEN** its name shows as plain text and the artwork is not a link

#### Scenario: The listener follows a link

- **WHEN** the listener clicks the artist's name while a track plays
- **THEN** the artist's page opens and the track keeps playing in the mini player
