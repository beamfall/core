## ADDED Requirements

### Requirement: Opted-in Entity Media lists roll held Media up to their container

With `rollUp: true`, `mediaForEntity` SHALL replace each non-audio Media item a declared container
holds with one Entity card for its outermost container, listed once at its first item's position,
and SHALL keep Media no container holds. Media whose container chain includes the context Entity,
and audio Media, SHALL stay listed. Without the argument the read SHALL return every Media item.

#### Scenario: Person credited on held Media

- **WHEN** a person is credited on twelve Media held by two parts of one volume and on one Media
  no container holds, and a client reads `mediaForEntity` with `rollUp: true`
- **THEN** the response lists one card for the volume and the standalone Media item

#### Scenario: A container's own children stay listed

- **WHEN** a client reads a part's `mediaForEntity` with `rollUp: true`
- **THEN** the response lists the Media the part holds

#### Scenario: Argument omitted

- **WHEN** a client reads `mediaForEntity` without `rollUp`
- **THEN** the response lists every Media item the Entity relates to

### Requirement: Rolled-up container cards carry a year and watch counts

A rolled-up container card SHALL carry the earliest year of the Media it stands for and the
child watch counts every container card carries.

#### Scenario: Year from the earliest member

- **WHEN** a container card stands for Media from 2011 and 2014
- **THEN** its `year` is 2011

### Requirement: Linked class spans are read once per request

An Entity card with no Media of its own SHALL take the class its linked Entities' Media share,
reading each linked Entity's Media once per request rather than once per card.

#### Scenario: Cards sharing a heavily linked Entity

- **WHEN** forty container cards each link the same person credited on thousands of Media
- **THEN** each card takes the class its linked Entities' Media share and the Entity read stays
  within one class-span pass over that person's Media

### Requirement: Web Entity pages offer watch actions only on containers

The web Entity page SHALL show Mark watched, the watchlist toggle and the rating only for
container-archetype Entities. Person and category pages SHALL keep Hide, Play and On Deck.

#### Scenario: Category page

- **WHEN** a viewer opens a category Entity's page
- **THEN** the page shows Hide and no Mark watched, watchlist or rating control

#### Scenario: Container page

- **WHEN** a viewer opens a container Entity's page
- **THEN** the page shows Mark watched, the watchlist toggle and the rating

### Requirement: Web Entity pages list rolled-up containers

The web Entity page SHALL request `mediaForEntity` with `rollUp: true`, count the remaining
children of rolled-up container cards in its unwatched count, and play the next unfinished
standalone Media item, otherwise the next-up child of the first rolled-up container with
children left.

#### Scenario: Person credited only on held Media

- **WHEN** a viewer opens a person page whose Media all sit in containers
- **THEN** the page lists one card per container and its Play action plays a child of the first
  container with children left
