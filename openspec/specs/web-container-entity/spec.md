# web-container-entity Specification

## Purpose
Defines the web Viewer page for a container Entity: what it leads with, how it lists its children and how it hands them to the player, using only the labels the owning plugin declares.

## Requirements

### Requirement: Container Entity page leads with the next-up child

The web Entity page SHALL show a lockup of the title, a chip with the plugin's display name for the Entity's kind, and only viewer-facing fields under their declared labels. The page SHALL read the Entity's ordered Media for its primary action whether or not a detail Layout renders them. When the Entity has a playable child, it SHALL offer one accent-filled action that plays the first child in progress, else the first unfinished child, else the first child. Locked children are skipped. The player SHALL receive the container's order as its queue. When the Entity has no playable Media of its own, the action SHALL play the next-up child of its next-up child container: the first related group whose archetype is a container, then the first unlocked item in it with children left to finish, else its first unlocked item, with that child container's order as the queue. The page SHALL NOT name any plugin kind to decide this.

#### Scenario: A child is in progress
- **GIVEN** a container whose second child has a saved position
- **WHEN** its Entity page loads
- **THEN** the only accent-filled control is Resume for the second child
- **AND** choosing it opens the player with all the container's children as the queue

#### Scenario: Nothing is in progress
- **GIVEN** a container whose first child is finished and second is not started
- **THEN** the action is Play for the second child

#### Scenario: A container of containers
- **GIVEN** a container with a detail Layout, no Media of its own, and two related child containers, the first finished and the second with children left
- **WHEN** its Entity page loads
- **THEN** the only accent-filled control plays the second child container's next-up child
- **AND** the player receives the second child container's order as its queue

### Requirement: Container children are grouped, ordered links

The Entity page SHALL render its Media children as ordered links grouped by item kind in first-seen order, each showing a thumbnail, its number, title, runtime and progress or watched state. Document order SHALL match visual order, so keyboard focus follows it. On a phone width the children SHALL render as list rows.

#### Scenario: Keyboard order
- **WHEN** a container lists three children
- **THEN** their links appear in the server's order with no positive tabindex

#### Scenario: Two kinds of child
- **WHEN** the children carry two kinds
- **THEN** each kind is a headed group in the order the kinds first appear

### Requirement: An empty detail Layout renders nothing inside an Entity

When every Section of an Entity's detail Layout is empty, or the Layout has no Sections, the web Entity page SHALL render nothing for the Layout. It SHALL NOT show the library empty or onboarding state (first scan, migrate) inside an Entity page.

#### Scenario: Owner opens a container whose detail Layout is empty
- **GIVEN** a manager and an Entity whose detail Layout's only Section returns no items
- **WHEN** the Entity page loads
- **THEN** no "Start first scan" or "Migrate" action appears on it
