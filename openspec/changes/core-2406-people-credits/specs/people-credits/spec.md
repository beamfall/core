## ADDED Requirements

### Requirement: Credits carry relation attributes and artwork

Each item of `media(id).credits` SHALL carry the relation's non-blank attributes as `attributes`
and the credited Entity's stored artwork slots as `artworkSlots`, each omitted when empty. Within
one relation, credits SHALL be ordered by the relation's sort order, rows without one last, then by
title.

#### Scenario: Billed credit with a headshot

- **WHEN** a Media item is credited to a person Entity with relation attributes
  `{"character": "The Hero"}`, sort order 0, and a stored poster
- **THEN** that credit carries those attributes and an `artworkSlots.poster` URL, and comes before
  a credit with sort order 1, whatever their titles

#### Scenario: Bare credit

- **WHEN** a credit has no attributes, or only blank ones, and the Entity has no artwork
- **THEN** the credit omits `attributes` and `artworkSlots`

### Requirement: Entity detail carries the Entity's artwork

`entity(id)` SHALL carry `artworkSlots`, the Entity's own stored artwork slots mapped to versioned
URLs, omitted when it has none.

#### Scenario: Person headshot

- **WHEN** a client reads `entity(id)` for an Entity with a stored poster
- **THEN** `artworkSlots.poster` is the versioned URL of that poster

### Requirement: Web cast row on the Media detail page

The web Media detail page SHALL show the Media item's person-archetype credits as a row of cards
in credit order. Each card SHALL show the headshot (or the name's initial when there is none), the
name, and the credit's character or role attribute when present, and SHALL link to the person
page. Credits with a `department` attribute SHALL be grouped under that department.

#### Scenario: Cast with characters

- **WHEN** a video Media item has person credits with character attributes
- **THEN** the page shows a cast row listing each person with the character beneath the name

#### Scenario: No person credits

- **WHEN** a Media item has no person-archetype credits
- **THEN** no cast row is shown

### Requirement: Web person page photo and filmography

The web person page SHALL show the Entity's poster as a headshot beside its name when it has one.
It SHALL list the Media the person is credited on from the viewer's Libraries, newest year first
and undated items last.

#### Scenario: Filmography order

- **WHEN** a person is credited on Media from 1999, 2015 and an undated item
- **THEN** the page lists the 2015 item first, then the 1999 item, then the undated item
