# people-credits Specification

## Purpose
Media credits expose each person's role attributes and artwork in a stable order, so clients can
show cast and crew with photos and character names the way other media servers do.

## Requirements

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
page. Credits with a `department` attribute SHALL be grouped under a heading naming that
department, even when every credit shares one department.

#### Scenario: Cast with characters

- **WHEN** a video Media item has person credits with character attributes
- **THEN** the page shows a cast row listing each person with the character beneath the name

#### Scenario: One department

- **WHEN** every person credit carries the same `department` attribute
- **THEN** the cast row shows that department as a heading above its cards

#### Scenario: No person credits

- **WHEN** a Media item has no person-archetype credits
- **THEN** no cast row is shown

### Requirement: Web cast row for audio Media

Audio Media SHALL show the cast row too when any person credit carries a headshot or a part
(character, role or job); it then SHALL NOT also name its people under the title, so a name is
never listed twice. Audio Media whose person credits carry neither SHALL name them, linked, under
the title instead of in a cast row.

#### Scenario: Audio item with headshots or parts

- **WHEN** an audio Media item has a person credit with a headshot or a role
- **THEN** the page shows the cast row and names no people under the title

#### Scenario: Audio item with names only

- **WHEN** an audio Media item's person credits carry no headshot and no part
- **THEN** each person is named once, linked under the title, and no cast row is shown

### Requirement: Web person page photo and filmography

The web person page SHALL show the Entity's poster as a headshot beside its name when it has one.
It SHALL list the Media the person is credited on from the viewer's Libraries, newest year first
and undated items last.

#### Scenario: Filmography order

- **WHEN** a person is credited on Media from 1999, 2015 and an undated item
- **THEN** the page lists the 2015 item first, then the 1999 item, then the undated item

### Requirement: Opted-in Entity Media rails fold edition groups

The `entity-media` Source SHALL fold edition groups only for rail reads that pass non-empty
`excludeEditionGroupOf`. Those reads SHALL return one visible, non-trashed representative
per edition group before limit and offset. It SHALL prefer a visible parent, otherwise the visible
member with the lowest Media ID, under the same Library, reveal and certification filter.
At least one visible member SHALL carry the queried credit. The representative SHALL be
independent of the queried credit.

#### Scenario: Visible parent

- **WHEN** a rail passes `excludeEditionGroupOf` and a visible parent or its editions carry a person credit
- **THEN** the Source returns only the parent card for that group

#### Scenario: Hidden parent

- **WHEN** a rail passes `excludeEditionGroupOf` and the parent is hidden but two editions are visible
- **THEN** the Source returns only the visible edition with the lowest Media ID

#### Scenario: Different credits on sibling editions

- **WHEN** rails pass `excludeEditionGroupOf` and a hidden parent's visible editions carry different category credits
- **THEN** each credit Source returns the same visible group representative

#### Scenario: Paged groups

- **WHEN** a rail passes `excludeEditionGroupOf` and requests successive pages of a person Source containing editions
- **THEN** limit and offset count group representatives rather than discarded editions

### Requirement: Edition groups support connected relations

For rail reads passing non-empty `excludeEditionGroupOf`, connected `edition-of` relations
SHALL form a group, including chains and cycles. Multiple visible terminal parents SHALL use the lowest Media ID. A group without a visible terminal
parent SHALL use its lowest visible Media ID.

#### Scenario: Chained or cyclic relations

- **WHEN** a rail passes `excludeEditionGroupOf` and edition relations form a chain or cycle
- **THEN** the Source terminates and returns one deterministic representative for the group

### Requirement: Plain Entity and browse reads retain their contents

Plain `entity-media` Source reads with the
argument omitted or empty (container, person and category pages), internal `MediaForEntity`
reads and `all-items` Library browse SHALL retain their existing behavior. Whether browse
and Entity pages should fold editions is a separate decision.

#### Scenario: Ordered playback children

- **WHEN** a playback seed reads an Entity containing a parent and its editions
- **THEN** `MediaForEntity` retains all visible children in their existing order

#### Scenario: Plain Entity Source preserves an edition child

- **WHEN** an Entity contains only edition E, its parent P is not related to that Entity, and `excludeEditionGroupOf` is omitted or empty
- **THEN** the `entity-media` Source returns E and agrees with `MediaForEntity`

### Requirement: More-with excludes the current edition group on the server

The `entity-media` Source SHALL accept optional `excludeEditionGroupOf` naming a visible
Media, fold edition groups to one representative and omit the context Media's whole edition
group before pagination. An unknown or hidden context SHALL produce the same error. The web More-with rail SHALL supply its detail Media ID.

#### Scenario: More-with on an edition

- **WHEN** More-with is requested from an edition's detail page
- **THEN** the Source excludes the edition, its parent and its sibling editions

#### Scenario: Hidden exclusion context

- **WHEN** the supplied exclusion context is unknown or hidden
- **THEN** the Source returns an indistinguishable error
