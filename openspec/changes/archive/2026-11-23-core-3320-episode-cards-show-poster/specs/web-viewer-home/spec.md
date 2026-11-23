## MODIFIED Requirements

### Requirement: Recently Added folds by container without hiding other items

The Viewer SHALL read 100 recent items for a Recently Added shelf, fold items sharing a container
into one container card, and show up to 20 cards. A video that is the only new item in its container
SHALL also fold into its container card; any other item alone in its container SHALL stay as it is.
A folded card SHALL count the items the Profile has not finished and SHALL name no count when every
item is finished.

#### Scenario: One show adds a whole season

- **WHEN** 40 episodes of one show are newer than 12 films
- **THEN** the shelf shows the show's card with "40 new" and all 12 films

#### Scenario: Every new item is finished

- **WHEN** every new item of a folded container is finished
- **THEN** its card names no count

#### Scenario: One show adds one episode

- **WHEN** a show's only new item is one episode
- **THEN** the shelf shows the show's card with "1 new" and the show's poster, and it opens the show

#### Scenario: One album adds one track

- **WHEN** an album's only new item is one track
- **THEN** the shelf shows the track's own card

## ADDED Requirements

### Requirement: Continue Watching draws the container poster

The Viewer SHALL draw a Continue Watching item with its container's poster when the container has
one, and SHALL keep the item's own target and resume position.

#### Scenario: An episode is in progress

- **WHEN** an episode of a show with a poster is in Continue Watching
- **THEN** its card draws the show's poster and opens the episode to resume
