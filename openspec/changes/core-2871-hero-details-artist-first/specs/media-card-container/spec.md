## MODIFIED Requirements

### Requirement: Audio cards are headed by their own title

Outside the container's own page, a card for an audio Media item held by a container SHALL be
headed by the item's own title and lead with the container's title; a subtitle such as a track
number SHALL NOT replace the container. The line under the heading SHALL name the item's byline
before the container, so truncating a long container title keeps the artist. Video items held by a
container SHALL keep the container's title as the heading.

#### Scenario: A track on Home

- **WHEN** a track "Shallows" on album "Low Tide" by "Nightglass" appears on Home
- **THEN** its card is headed "Shallows" with the line "Nightglass · Low Tide"
