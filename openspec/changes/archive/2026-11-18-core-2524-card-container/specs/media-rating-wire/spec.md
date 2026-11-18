## ADDED Requirements

### Requirement: A zero or vote-less audience score is no score

The server SHALL treat a stored `community-rating` of zero or less, or one whose stored
`community-rating-votes` is zero, as no audience score: cards, search rows and the `media(id)` read
SHALL omit `audienceScore`, `audienceScoreLabel` and `audienceScoreVotes` for it. A score with no
stored vote count SHALL be kept.

#### Scenario: Zero score

- **WHEN** an item stores `community-rating` 0
- **THEN** its card carries no `audienceScore`

#### Scenario: Zero votes

- **WHEN** an item stores `community-rating` 7.5 and `community-rating-votes` 0
- **THEN** its card carries no `audienceScore`
