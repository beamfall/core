# media-card-container Specification

## Purpose
Media cards and search rows name the outermost container (series, album, collection) holding a Media item, so a bare episode or track title still says where it belongs.

## Requirements

### Requirement: Cards name the outermost container holding a Media item

Every Graph read that returns `SourceItem` cards, and search result rows, SHALL set `container` on a
Media item held by a container-archetype Entity of its own Library to the `id`, `title` and stored
artwork of the outermost such Entity. A Media item with no such holder, and every Entity item, SHALL
omit the field.

#### Scenario: Nested containers

- **WHEN** a Media item is held through a single-holder relation by container Entity A, and A points
  through a declared container relation at container Entity B
- **THEN** the item's card carries `container` with B's id and title

### Requirement: The container chain follows declared container relations

The innermost container SHALL be the container Entity whose kind declares a single-holder relation
to the Media; each next one SHALL be the container Entity the previous points at through a relation
its kind declares to another container kind.

#### Scenario: Non-holding container relation

- **WHEN** a Media item is linked only from container Entities whose relation to it is not
  single-holder
- **THEN** the item's card carries no `container`

### Requirement: Outermost container kinds are title-searchable

The server SHALL make every container-archetype Entity kind at the outer end of a container chain
searchable by title in keyword search, whether or not its plugin opts it into full text search: a
kind that holds Media or that another container kind points at, and that itself points at no other
container kind. A container kind in no chain keeps its plugin's choice.

#### Scenario: Searching a container title

- **WHEN** a Library holds an outermost container Entity titled "Ted Lasso" and the viewer searches
  "ted lasso"
- **THEN** the keyword result includes that Entity

### Requirement: Keyword search orders items within their container

Keyword search SHALL rank a Media item that carries a `container` by the better of its own title
match and its container's title match. Rows of equal rank SHALL be ordered by container title (an
Entity's own title, an item's container title, else its own title), then the container Entity
before the items it holds, then the items' sequence and index.

#### Scenario: Container first, items in order

- **WHEN** the viewer searches a container's exact title and the result holds that container and
  three of its items
- **THEN** the container row comes first and its items follow in sequence, then index, order

### Requirement: Audio cards are headed by their own title

Outside the container's own page, a card for an audio Media item held by a container SHALL be
headed by the item's own title and lead with the container's title; a subtitle such as a track
number SHALL NOT replace the container. The line under the heading SHALL name the item's byline
before the container, so truncating a long container title keeps the artist. Video items held by a
container SHALL keep the container's title as the heading.

#### Scenario: A track on Home

- **WHEN** a track "Shallows" on album "Low Tide" by "Nightglass" appears on Home
- **THEN** its card is headed "Shallows" with the line "Nightglass · Low Tide"
