## MODIFIED Requirements

### Requirement: Person pages separate own containers from appearances

An Entity of archetype `person` whose Media are all audio SHALL list its Media in Profile
play order first and fill remaining places in server order. The section SHALL read “Tracks”
when there is no available play ranking and “Top tracks” when there is one. The viewer
SHALL list separately the containers credited on its Media that are not related to the person.

#### Scenario: Artist with no played tracks

- **WHEN** none of the person's listed tracks have plays reported to the viewer
- **THEN** the section is titled “Tracks” and the preview follows server order

#### Scenario: Artist with played tracks

- **WHEN** the Profile's play ranking contains tracks by this person
- **THEN** those tracks precede the unranked tracks and the section is titled “Top tracks”

#### Scenario: Appears on

- **WHEN** a person's Media are credited on a container the person is not related to
- **THEN** that container is listed under “Appears on” and the person's own containers are not

### Requirement: Listening wording for audio Media

The web viewer SHALL use listening wording in audio Media card menus and item actions.
On an audio Entity listing, the remaining count SHALL read “unplayed” and the item
selected for the primary Play or Resume action SHALL be labelled “Up next”. Audio item
actions SHALL omit the watchlist button. Video surfaces SHALL retain watching wording
and the watchlist button.

#### Scenario: Audio card menu

- **WHEN** a listener opens the menu of an audio Media card
- **THEN** the entry reads “Mark played”

#### Scenario: Audio item actions

- **WHEN** a listener opens an audio item with no completed play
- **THEN** the action reads “Mark played” and the watchlist button is absent

## ADDED Requirements

### Requirement: Audio cards expose credited people and containers

For an audio Media item, the card menu SHALL link to the first available person credit
and first available container credit. For an audio container Entity, the card subtitle
SHALL show the known credited people and release year, in that order when both are known.

#### Scenario: Track with a person and container credit

- **WHEN** a listener opens the track's menu
- **THEN** “Go to artist” and “Go to album” lead to the corresponding Entity pages

#### Scenario: Container with artist and release date

- **WHEN** the viewer has both values for an audio container card
- **THEN** its subtitle shows the artist followed by the release year
