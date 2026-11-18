# media-rating-wire Specification

## Purpose
Carry an item's content rating (certification) and audience score on the client wire, so clients can show them on browse cards and the media detail read without extra round-trips (CORE-2398).

## Requirements

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

### Requirement: Cards and the media read name the audience score's source

Every Graph read that returns `SourceItem` cards, and the `media(id)` read, SHALL set
`audienceScoreLabel` on a Media item that carries `audienceScore` to the label its Library plugin
declares for the `community-rating` Media field. An item without `audienceScore`, a plugin-less
Library, and a plugin that declares no label SHALL omit the field.

#### Scenario: Plugin-declared label

- **WHEN** an item in a Library whose plugin labels `community-rating` "TMDB rating" stores
  `community-rating` 7.3
- **THEN** its card and its `media(id)` read carry `audienceScoreLabel` with `en` "TMDB rating"

#### Scenario: Plugin-less Library

- **WHEN** an item in a Library bound to no plugin stores `community-rating` 7.3
- **THEN** its card carries `audienceScore: 7.3` and no `audienceScoreLabel` key

### Requirement: Cards and the media read carry the audience score's vote count

Every Graph read that returns `SourceItem` cards, and the `media(id)` read, SHALL set
`audienceScoreVotes` on a Media item that carries `audienceScore` to its `community-rating-votes`
number field when that value is positive. Otherwise the field SHALL be omitted.

#### Scenario: Votes present

- **WHEN** an item stores `community-rating` 7.3 and `community-rating-votes` 1200
- **THEN** its card and its `media(id)` read carry `audienceScoreVotes: 1200`

#### Scenario: No score

- **WHEN** an item stores `community-rating-votes` 1200 but no usable `community-rating`
- **THEN** its card has neither `audienceScoreLabel` nor `audienceScoreVotes`
