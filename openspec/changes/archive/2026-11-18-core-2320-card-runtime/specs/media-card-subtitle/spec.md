## ADDED Requirements

### Requirement: Shelf cards show known runtime

The web viewer SHALL request a Media card's stored durationSeconds from its Source and SHALL display a positive runtime on poster and thumbnail cards as well as list rows. It SHALL omit absent or zero durations and SHALL NOT show a child runtime on a grouped container card.

#### Scenario: Recently added item with runtime

- **WHEN** an individual Media card on a shelf has durationSeconds 1830
- **THEN** the card displays 30:30 alongside its existing title and subtitle

#### Scenario: Grouped container

- **WHEN** several new children fold into one container card
- **THEN** the card states the new-item count without borrowing a child's duration
