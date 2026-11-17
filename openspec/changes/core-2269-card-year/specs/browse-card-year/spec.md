## ADDED Requirements

### Requirement: A browse card carries its item's year

Every Graph read that returns `SourceItem` cards SHALL set `year` on a Media card to the item's
stored year, as an integer. A Media card whose item has no year, and every Entity card, SHALL omit
the field.

#### Scenario: Item with a year

- **WHEN** a client reads `sourceItems` and one returned item has the year 2019
- **THEN** that card carries `year: 2019`

#### Scenario: Item without a year

- **WHEN** a returned item has no stored year
- **THEN** its card has no `year` key

#### Scenario: Entity card

- **WHEN** a read returns an Entity card
- **THEN** the card has no `year` key

#### Scenario: Semantic mode

- **WHEN** a client reads `sourceItems` in semantic mode and a returned item has a year
- **THEN** its card carries the same `year` and `mediaClass` that the keyword read returns for it

### Requirement: The year does not change what a session is told about restricted items

Adding `year` SHALL NOT change which cards a session receives, and SHALL NOT change the
`restricted`, `locked` or `muted` fields of a card.

#### Scenario: Session that has not revealed restricted libraries

- **WHEN** a session that has not revealed restricted libraries reads `sourceItems`
- **THEN** it receives no card from a restricted library and no card carries a `restricted` key

#### Scenario: Locked card

- **WHEN** a card is returned with `locked: true`
- **THEN** it is still returned with `locked: true`, with or without a `year`
