## ADDED Requirements

### Requirement: Container Entity page leads with the next-up child

The web Entity page SHALL show a lockup of the title, a chip with the plugin's display name for the Entity's kind, and only viewer-facing fields under their declared labels. When the page lists the Entity's ordered Media, it SHALL offer one accent-filled action that plays the first child in progress, else the first unfinished child, else the first child. Locked children are skipped. The player SHALL receive the container's order as its queue.

#### Scenario: A child is in progress
- **GIVEN** a container whose second child has a saved position
- **WHEN** its Entity page loads
- **THEN** the only accent-filled control is Resume for the second child
- **AND** choosing it opens the player with all the container's children as the queue

#### Scenario: Nothing is in progress
- **GIVEN** a container whose first child is finished and second is not started
- **THEN** the action is Play for the second child

### Requirement: Container children are grouped, ordered links

The Entity page SHALL render its Media children as ordered links grouped by item kind in first-seen order, each showing a thumbnail, its number, title, runtime and progress or watched state. Document order SHALL match visual order, so keyboard focus follows it. On a phone width the children SHALL render as list rows.

#### Scenario: Keyboard order
- **WHEN** a container lists three children
- **THEN** their links appear in the server's order with no positive tabindex

#### Scenario: Two kinds of child
- **WHEN** the children carry two kinds
- **THEN** each kind is a headed group in the order the kinds first appear
