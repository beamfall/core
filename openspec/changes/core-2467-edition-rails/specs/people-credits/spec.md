## ADDED Requirements

### Requirement: Opted-in Entity Media rails fold edition groups

The `entity-media` Source SHALL fold edition groups only for rail reads that pass non-empty
`excludeEditionGroupOf`. Those reads SHALL return one visible, non-trashed representative
per edition group before limit and offset. It SHALL prefer a visible parent, otherwise the visible
member with the lowest Media ID, under the same Library, reveal and certification filter.
At least one visible member SHALL carry the queried credit. The representative SHALL be
independent of the queried credit.

#### Scenario: Visible parent

- **WHEN** a rail passes `excludeEditionGroupOf` and a visible parent or its editions carry a person credit
- **THEN** the Source returns only the parent card for that group

#### Scenario: Hidden parent

- **WHEN** a rail passes `excludeEditionGroupOf` and the parent is hidden but two editions are visible
- **THEN** the Source returns only the visible edition with the lowest Media ID

#### Scenario: Different credits on sibling editions

- **WHEN** rails pass `excludeEditionGroupOf` and a hidden parent's visible editions carry different category credits
- **THEN** each credit Source returns the same visible group representative

#### Scenario: Paged groups

- **WHEN** a rail passes `excludeEditionGroupOf` and requests successive pages of a person Source containing editions
- **THEN** limit and offset count group representatives rather than discarded editions

### Requirement: Edition groups support connected relations

For rail reads passing non-empty `excludeEditionGroupOf`, connected `edition-of` relations
SHALL form a group, including chains and cycles. Multiple visible terminal parents SHALL use the lowest Media ID. A group without a visible terminal
parent SHALL use its lowest visible Media ID.

#### Scenario: Chained or cyclic relations

- **WHEN** a rail passes `excludeEditionGroupOf` and edition relations form a chain or cycle
- **THEN** the Source terminates and returns one deterministic representative for the group

### Requirement: Plain Entity and browse reads retain their contents

Plain `entity-media` Source reads with the
argument omitted or empty (container, person and category pages), internal `MediaForEntity`
reads and `all-items` Library browse SHALL retain their existing behavior. Whether browse
and Entity pages should fold editions is a separate decision.

#### Scenario: Ordered playback children

- **WHEN** a playback seed reads an Entity containing a parent and its editions
- **THEN** `MediaForEntity` retains all visible children in their existing order

#### Scenario: Plain Entity Source preserves an edition child

- **WHEN** an Entity contains only edition E, its parent P is not related to that Entity, and `excludeEditionGroupOf` is omitted or empty
- **THEN** the `entity-media` Source returns E and agrees with `MediaForEntity`

### Requirement: More-with excludes the current edition group on the server

The `entity-media` Source SHALL accept optional `excludeEditionGroupOf` naming a visible
Media, fold edition groups to one representative and omit the context Media's whole edition
group before pagination. An unknown or hidden context SHALL produce the same error. The web More-with rail SHALL supply its detail Media ID.

#### Scenario: More-with on an edition

- **WHEN** More-with is requested from an edition's detail page
- **THEN** the Source excludes the edition, its parent and its sibling editions

#### Scenario: Hidden exclusion context

- **WHEN** the supplied exclusion context is unknown or hidden
- **THEN** the Source returns an indistinguishable error
