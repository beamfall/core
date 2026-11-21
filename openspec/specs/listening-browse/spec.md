# listening-browse Specification

## Purpose
Let a listener browse audio in the web viewer from generic signals core owns (Entity archetype,
Media class, server order and the scan's stored positions), never from plugin Kind names.

## Requirements

### Requirement: Audio container rows group and number by stored positions

When every Media listed by an audio container has a stored `sequence` field and they hold at least
two values, the web viewer SHALL group the rows by `sequence`, ascending, each group under its own
heading and list. Within a group, or the whole list, rows SHALL be numbered and ordered by stored
`index` when every row has a distinct one, else kept in server order and numbered from 1.

#### Scenario: Two discs

- **WHEN** a listener opens an audio container whose tracks store sequences 1 and 2 with indexes
- **THEN** the page shows a headed list per sequence, ascending, each numbered by stored index

#### Scenario: No stored sequence

- **WHEN** any listed track has no stored sequence, or all share one
- **THEN** the page shows one list without group headings

#### Scenario: Repeated index

- **WHEN** two tracks in a group store the same index
- **THEN** that group keeps server order and is numbered from 1

### Requirement: Play from a grouped listing queues every group

Starting playback from any row of an audio container's listing, or from the page's primary Play
action, SHALL use the listing's display order: the primary action starts the next-up track in that
order, and the player SHALL queue every listed row in display order, across groups.

#### Scenario: Play from the second disc

- **WHEN** a listener plays a track in the second group
- **THEN** the queue holds the first group's rows, then the second group's, in display order

#### Scenario: Primary Play with the second disc first in server order

- **WHEN** server order lists a second-disc track first and the listener uses the page's Play action
- **THEN** playback starts on the first disc's first track with the display order queued

### Requirement: Archetype and class drive the Entity listing layout

The web viewer SHALL choose an Entity page's Media layout only from the Entity archetype, the
Media class and server order, never from plugin Kind names. An Entity of archetype `container`
whose listed Media are all of class `audio` SHALL list them as numbered rows in server order, each
with its title, its runtime when known, and its person credits when they differ from the
container's own people. Every other Entity SHALL keep the poster grid.

#### Scenario: Audio container

- **WHEN** a listener opens a container Entity whose Media are all audio
- **THEN** the page lists the Media as numbered rows in server order with runtimes

#### Scenario: Mixed or non-audio container

- **WHEN** any listed Media is of another class
- **THEN** the page shows the poster grid

### Requirement: Play from a row queues the listing

Starting playback from a row SHALL open the player on that Media with the listed rows as the
play queue for the current viewer.

#### Scenario: Play the second row

- **WHEN** a listener plays the second row of a three-row listing
- **THEN** the player opens on that Media with all three rows queued in order

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

### Requirement: Playlists from the viewer

The viewer SHALL let a manager device create, rename, reorder, remove Media from and delete
playlists (Collections), and SHALL offer "Add to playlist" in every Media card menu.

#### Scenario: Reorder

- **WHEN** a manager moves a playlist's third Media up
- **THEN** the viewer stores the whole new order through the Collection reorder mutation

### Requirement: Favourites and history

`sourceItems` SHALL accept the kind `favorites`, returning the calling Profile's favourited items
newest first, and the viewer SHALL show favourites and recently and most played pages.

#### Scenario: Favourites page

- **WHEN** a listener favourites a Media and opens Favourites
- **THEN** the Media is listed

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
