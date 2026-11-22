## MODIFIED Requirements

### Requirement: A container of containers plays through its next-up child container

The page SHALL read the Entity's ordered Media for its primary action whether or not a detail Layout renders them. With no playable Media of its own, its primary action SHALL play the next-up child of its next-up child container: in the first related child group whose archetype is a container, the first unlocked item with children left to finish, else its first unlocked item. A parent group SHALL NOT be read for it, wherever it sorts. The player SHALL receive that child container's order as its queue. The page SHALL NOT name a plugin kind to choose it.

#### Scenario: A container of containers
- **GIVEN** a container with a detail Layout, no Media of its own, and two related child containers, the first finished and the second with children left
- **WHEN** its Entity page loads
- **THEN** the only accent-filled control plays the second child container's next-up child
- **AND** the player receives the second child container's order as its queue

#### Scenario: A middle container with a parent container sorting first
- **GIVEN** a container with no Media of its own, a related parent container group sorting first and a related child container group
- **WHEN** its Entity page loads
- **THEN** its primary action plays through the child container, never the parent
