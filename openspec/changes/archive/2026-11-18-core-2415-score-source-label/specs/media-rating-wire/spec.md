## ADDED Requirements

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
