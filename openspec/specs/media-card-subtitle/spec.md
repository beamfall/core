# media-card-subtitle Specification

## Purpose
Browse cards carry the plugin's subtitle and ordinals, such as an episode number and title, so users can tell similar items apart and choose how cards are labelled.

## Requirements

### Requirement: Media cards carry the plugin's subtitle and ordinals

Every Graph read that returns Media cards (`SourceItem`) SHALL carry `subtitle`, the Library
plugin's `MediaSubtitle` template rendered for that item in each locale the template declares,
and the numeric `sequence` and `index` the item stores. Core SHALL render the template through the
plugin SDK without interpreting its text. A Media card SHALL also carry `durationSeconds`, its
stored runtime, so a compact list row can state it. Each key SHALL be omitted when it has no value.

#### Scenario: Numbered item with a title field

- **WHEN** a plugin declares the segments `S{sequence}`, `E{index}` and `{part-title}`, and an item
  stores sequence 5, index 3 and part-title "Reunion"
- **THEN** its card carries `subtitle` "S5 · E3 · Reunion", `sequence` 5 and `index` 3

#### Scenario: Stored runtime

- **WHEN** a Media item stores a runtime of 1800 seconds
- **THEN** its card carries `durationSeconds` 1800

#### Scenario: Missing value

- **WHEN** the same item stores no part-title
- **THEN** its card carries `subtitle` "S5 · E3"

#### Scenario: No template

- **WHEN** the Library's plugin declares no `MediaSubtitle`
- **THEN** its cards carry no `subtitle`

### Requirement: Shelf cards show known runtime

The web viewer SHALL request a Media card's stored durationSeconds from its Source and SHALL display a positive runtime on poster and thumbnail cards as well as list rows. It SHALL omit absent or zero durations and SHALL NOT show a child runtime on a grouped container card.

#### Scenario: Recently added item with runtime

- **WHEN** an individual Media card on a shelf has durationSeconds 1830
- **THEN** the card displays 30:30 alongside its existing title and subtitle

#### Scenario: Grouped container

- **WHEN** several new children fold into one container card
- **THEN** the card states the new-item count without borrowing a child's duration
