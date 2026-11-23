## ADDED Requirements

### Requirement: People row heading follows the credit kind

Each media credit SHALL carry `kindPluralDisplayName`, its kind's declared plural display name,
when the kind declares one. The media detail page SHALL head its row of person-archetype credits,
and name that list for assistive technology, with that plural display name in the viewer's locale
when every person-archetype credit on the item has the same kind and that kind declares a plural
display name. Otherwise the heading SHALL be the generic "People" label.

#### Scenario: All credits share a kind with a plural label

- **WHEN** an item's only person-archetype credits are of a kind whose plural display name is
  "Artists"
- **THEN** the row is headed "Artists"

#### Scenario: Mixed kinds fall back

- **WHEN** an item has person-archetype credits of two different kinds
- **THEN** the row is headed "People"

#### Scenario: Kind without a plural label falls back

- **WHEN** the person-archetype credits share one kind that declares no plural display name
- **THEN** the row is headed "People"
