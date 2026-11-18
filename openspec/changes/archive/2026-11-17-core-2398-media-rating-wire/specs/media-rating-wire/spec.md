## ADDED Requirements

### Requirement: Cards and the media read carry a content rating

Every Graph read that returns `SourceItem` cards, and the `media(id)` read, SHALL set
`certification` on a Media item to its `certification-display` text field, or to its
`certification` text field when the display value is absent or empty. An item with neither, and
every Entity card, SHALL omit the field.

#### Scenario: Display rating wins

- **WHEN** an item stores `certification` "TV-14" and `certification-display` "TV-MA"
- **THEN** its card and its `media(id)` read carry `certification: "TV-MA"`

#### Scenario: Certification only

- **WHEN** an item stores only `certification` "PG-13"
- **THEN** its card carries `certification: "PG-13"`

#### Scenario: Unrated item

- **WHEN** an item stores neither field
- **THEN** its card has no `certification` key

### Requirement: Cards and the media read carry an audience score

Every Graph read that returns `SourceItem` cards, and the `media(id)` read, SHALL set
`audienceScore` on a Media item to its `community-rating` number field when the value is from 0
to 10. Otherwise the field SHALL be omitted.

#### Scenario: Score present

- **WHEN** an item stores `community-rating` 7.3
- **THEN** its card and its `media(id)` read carry `audienceScore: 7.3`

#### Scenario: Score off the scale

- **WHEN** an item stores `community-rating` 73
- **THEN** its card has no `audienceScore` key

### Requirement: The demo seed carries ratings

The demo seed SHALL give its films a `certification` and its series a `certification-display`,
each with a `community-rating`, so a client can show ratings against `beamfall serve --seed=demo`.

#### Scenario: Demo film

- **WHEN** a client reads the demo film "Sintel"
- **THEN** it carries `certification: "PG-13"` and an `audienceScore`
