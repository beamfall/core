## ADDED Requirements

### Requirement: Library Entity listing by browse kind

The Graph read `libraryEntities` SHALL return, for one Library and the calling session, the kinds
the Library can be browsed by, each with its plugin-declared Section title as `label`, its display
name, archetype and `total`, and one page of that Library's Entities of one kind, in title order.
When the request names no kind the read SHALL list the first browse kind.

#### Scenario: Paging keeps the total

- **WHEN** a client requests `libraryEntities` with `limit: 1, offset: 1` for a kind with N
  Entities
- **THEN** the response holds the second Entity in title order and `total` is N

#### Scenario: Library outside the session scope

- **WHEN** a session requests `libraryEntities` for a Library it cannot see
- **THEN** `kinds` and `items` are empty and `total` is 0

### Requirement: Browse kinds come from plugin declarations

The browse kinds SHALL be the Entity kinds the Library plugin's browse Layouts list through an
`entity-items` Section, ordered so that a kind comes before every browse kind declaring a relation
to it, and SHALL include only kinds with at least one Entity in the Library.

#### Scenario: Parent kind first

- **WHEN** a Library's plugin lists kinds A and B in browse Sections and kind B declares a relation
  to kind A
- **THEN** `kinds` lists A before B and a request without `kind` lists A's Entities

#### Scenario: No browse kinds

- **WHEN** the Library's plugin lists no Entity kind in a browse Section, or the Library holds no
  Entity of those kinds
- **THEN** `kinds` is empty

### Requirement: Listed Entities carry counts and parents

Each listed Entity SHALL carry `childCount`, the number of visible linked Entities of the kinds
that declare a relation to its kind; `mediaCount`, the number of distinct visible Media linked to
it or to one of those children, after the certification ceiling; and up to three `parents`, the
linked Entities of browse kinds its kind declares a relation to.

#### Scenario: Counts through children

- **WHEN** an Entity has two child Entities, each linked to three Media, and one Media of its own
- **THEN** its `childCount` is 2 and its `mediaCount` is 7

#### Scenario: Hidden Media are not counted

- **WHEN** one of an Entity's Media is above the session's certification ceiling
- **THEN** `mediaCount` does not count it

#### Scenario: Parent byline

- **WHEN** kind B declares a relation to browse kind A and a B Entity links to an A Entity
- **THEN** the B item's `parents` names that A Entity

### Requirement: Card class and byline for audio

An Entity card SHALL carry `mediaClass` when every visible Media linked to the Entity shares one
class. An audio Media card SHALL carry `byline`, the titles of the person-archetype Entities linked
to it, in link order. Other cards SHALL omit `byline`.

#### Scenario: Audio container card

- **WHEN** a read returns a card for an Entity whose linked Media are all audio
- **THEN** the card carries `mediaClass: "audio"`

#### Scenario: Track byline

- **WHEN** a read returns an audio Media card linked to a person-archetype Entity titled "P"
- **THEN** the card carries `byline: ["P"]`

### Requirement: Web Library page switches between kind views and the Media view

The web Library page SHALL offer one view per browse kind, labelled by the kind's `label`, and the
Media view, labelled by `allLabel` or a core fallback. It SHALL open on the first browse kind, keep
the chosen view in the URL, and show the Media view alone when there are no browse kinds. A kind
view SHALL render a paged grid of Entity cards whose line names the parents, else the Media count.
The switcher SHALL be keyboard operable and fit a 375px viewport.

#### Scenario: Music Library

- **WHEN** a viewer opens a Library whose browse kinds are A then B
- **THEN** the page opens on A's grid with switches for A, B and the Media view

#### Scenario: Shared link

- **WHEN** a viewer opens the page with a `view` naming B
- **THEN** B's grid shows

### Requirement: Square art for audio cards

A web card for an audio Media item, or for an Entity whose `mediaClass` is audio, SHALL frame its
art square (1:1), wherever the card renders, and name the item's byline under its title.

#### Scenario: Home row

- **WHEN** a Home row lists an audio Media item
- **THEN** its card's art frame is square

### Requirement: Audio container page offers Shuffle and names its people

An Entity page for an audio container SHALL offer Shuffle beside Play, starting a shuffled queue
of its tracks, and SHALL name its related person-archetype Entities under the title as links.

#### Scenario: Shuffle an album

- **WHEN** a viewer activates Shuffle on an audio container page
- **THEN** playback starts with a queue holding every track of the container

### Requirement: Listening layout over a Media-only detail Layout

An Entity page for a container- or person-archetype Entity whose Media all report class audio
SHALL list them in the listening layout (a container's numbered track list, a person's top tracks
followed by all its tracks) when its plugin detail Layout's Sections only list the Entity's own
Media. A detail Layout with any other Section, or an Entity with non-audio Media, SHALL keep the
detail Layout.

#### Scenario: Album with a track-list detail Layout

- **WHEN** an audio container's detail Layout holds a single entity-media list Section
- **THEN** the page shows the numbered track list with artist and duration instead of that Section

#### Scenario: Video container keeps its Layout

- **WHEN** a container whose Media are video has a detail Layout that only lists its Media
- **THEN** the page renders that detail Layout
