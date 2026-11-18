## REMOVED Requirements

### Requirement: A zero score needs votes

**Reason**: CORE-2524 reverses this part of CORE-2472. A 0 is a provider placeholder even when a
vote count sits beside it, so a card no longer shows "★ 0.0" for a voted 0.
**Migration**: Replaced by "A zero score is never shown". The server also omits such a score from
the wire (media-rating-wire), so clients need no change.

## ADDED Requirements

### Requirement: A zero score is never shown

A web card SHALL NOT show an audience score of 0 or less, whatever `audienceScoreVotes` it carries.

#### Scenario: Zero without votes

- **WHEN** a card has `audienceScore` 0 and no `audienceScoreVotes`
- **THEN** it renders no "★" score

#### Scenario: Zero with votes

- **WHEN** a card has `audienceScore` 0 and `audienceScoreVotes` 3
- **THEN** it renders no "★" score
