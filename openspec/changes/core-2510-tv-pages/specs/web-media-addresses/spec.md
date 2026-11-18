## ADDED Requirements

### Requirement: Navigating cards are links

Every card that navigates SHALL be an anchor with an href to the page it opens, so modifier and
middle clicks open a new tab. Its More menu SHALL stay a separate control.

#### Scenario: A card has an href

- **WHEN** a related container card renders
- **THEN** it is a link whose href is that Entity's page

### Requirement: Entity pages have readable addresses

An Entity page address SHALL read `/entity/<words>-<token>`, where the token is the hex digest of a
content-derived Entity ID (`entity-` plus 32 hex digits) and the words are the folded title. A
middle container SHALL lead with its ancestor's words as a segment.

#### Scenario: Middle container address

- **WHEN** a container titled "Season 1" under "3 Body Problem" is shown
- **THEN** the address is `/entity/3-body-problem/season-1-<hex>`

### Requirement: Entity addresses resolve without a lookup

The client SHALL resolve the Entity from the token alone, SHALL keep resolving a bare
`/entity/<id>`, and SHALL rewrite the address in place once the Entity is known. A Revealed
session SHALL keep the bare-ID address, and an ID of any other shape SHALL stay bare.

#### Scenario: Bare address is rewritten

- **WHEN** `/entity/entity-<hex>` opens a container titled "3 Body Problem"
- **THEN** the address becomes `/entity/3-body-problem-<hex>` with no new history entry
