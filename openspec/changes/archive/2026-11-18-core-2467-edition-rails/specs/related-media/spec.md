## ADDED Requirements

### Requirement: Related rails exclude the requested edition group

`relatedMedia` SHALL omit the requested Media and every Media in its edition group,
including its parent and sibling editions. It SHALL use the edition-folded `entity-media`
Source by passing the requested Media ID as `excludeEditionGroupOf`, folding and excluding
the requested group before each credit's bounded read. Plain `entity-media` reads without
this argument remain unchanged; folding browse and Entity pages is a separate decision.

#### Scenario: Parent detail

- **WHEN** an edition points to the requested parent with an `edition-of` relation
- **THEN** neither the parent nor its editions appear in Related

#### Scenario: Edition detail

- **WHEN** the requested Media is an edition of a parent with another edition
- **THEN** the parent, the requested edition and its sibling edition are absent from Related
