## ADDED Requirements

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
