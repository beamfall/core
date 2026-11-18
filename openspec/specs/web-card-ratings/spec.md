# web-card-ratings Specification

## Purpose
Web browse cards show a Media item's content rating and audience score at a glance, so ratings are
visible while browsing, not only on the detail page.

## Requirements

### Requirement: Browse cards show the content rating and audience score

A Media card in the web app SHALL show its `certification` as compact text and its `audienceScore`,
when from 0 to 10, as a star followed by the score with one decimal, on one line below the title
block. The card SHALL render no rating element when it has neither. The card button's accessible
name SHALL state what is shown, in the form "Title, Rated PG-13, audience score 7.8", naming only
the parts present. Entity cards, which carry neither field, SHALL be unchanged.

#### Scenario: Rating and score

- **WHEN** a card has `certification` "PG-13" and `audienceScore` 7.8
- **THEN** it shows "PG-13" and "★ 7.8" and its accessible name contains "Rated PG-13, audience score 7.8"

#### Scenario: Rating only

- **WHEN** a card has `certification` "TV-MA" and no `audienceScore`
- **THEN** it shows "TV-MA" and no score, and its accessible name contains "Rated TV-MA"

#### Scenario: Score only

- **WHEN** a card has `audienceScore` 6.5 and no `certification`
- **THEN** it shows "★ 6.5" and its accessible name contains "Audience score 6.5"

#### Scenario: Neither

- **WHEN** a card has neither field
- **THEN** it renders no rating element and its accessible name is unchanged

### Requirement: A zero score needs votes

A web card SHALL NOT show an audience score of 0 unless the card carries `audienceScoreVotes`
above zero.

#### Scenario: Zero without votes

- **WHEN** a card has `audienceScore` 0 and no `audienceScoreVotes`
- **THEN** it renders no "★" score
