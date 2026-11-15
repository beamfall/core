## ADDED Requirements

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

An Entity of archetype `person` whose Media are all audio SHALL list top tracks (the Profile's
ranking of its Media first, then server order) and SHALL list separately the containers credited
on its Media that are not related to the person.

#### Scenario: Appears on

- **WHEN** a person's Media are credited on a container the person is not related to
- **THEN** that container is listed under "Appears on" and the person's own containers are not

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

A Media card menu SHALL use listening wording ("Mark played", "Mark unplayed") when the Media's
class is `audio`, and watching wording otherwise.

#### Scenario: Audio card menu

- **WHEN** a listener opens the menu of an audio Media card
- **THEN** the entry reads "Mark played"
