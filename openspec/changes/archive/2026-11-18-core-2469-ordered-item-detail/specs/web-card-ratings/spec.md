## ADDED Requirements

### Requirement: A zero score needs votes

A web card SHALL NOT show an audience score of 0 unless the card carries `audienceScoreVotes`
above zero.

#### Scenario: Zero without votes

- **WHEN** a card has `audienceScore` 0 and no `audienceScoreVotes`
- **THEN** it renders no "★" score
