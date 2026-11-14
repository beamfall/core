## MODIFIED Requirements

### Requirement: Source items report the acting Profile's playback state

Every operation that returns `SourceItem` for a Profile SHALL include an `engagement` object on each
item the Profile has playback state for, and SHALL omit it otherwise. The server SHALL read that
state with one Profile-scoped store query per 500 Media IDs, never one query per item, and SHALL
never return another Profile's state.

#### Scenario: Partly played item

- **WHEN** the Profile has played an item past the resume floor without finishing it
- **THEN** its `engagement` carries `positionSeconds` and `durationSeconds` and no `completed`

#### Scenario: Finished item

- **WHEN** the Profile has finished an item
- **THEN** its `engagement` carries `completed: true`

#### Scenario: Ordered container

- **WHEN** an entity has ordered children the scope can see
- **THEN** its `engagement` carries `totalCount` and `remainingCount` over those children only, and
  `completed: true` once none remain

#### Scenario: Hidden children

- **WHEN** the scope cannot see any ordered child of an entity
- **THEN** the entity carries no `engagement`

#### Scenario: No Profile

- **WHEN** the request has no acting Profile
- **THEN** no item carries `engagement`

#### Scenario: Another Profile's progress

- **WHEN** only a different Profile has playback state for a Media item
- **THEN** that item carries no `engagement` for the acting Profile

#### Scenario: One query per page

- **WHEN** a page of up to 500 Media items is annotated
- **THEN** one progress query is issued for the page
