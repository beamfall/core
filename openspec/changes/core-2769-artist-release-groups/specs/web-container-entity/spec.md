## ADDED Requirements

### Requirement: The Entity page heads each section of a split kind

The web Entity page SHALL render a related container group that carries sections as one rail per
section, headed by the section's display name (or its value, or the group heading for items without
a value) with the section's item count, in the server's order.

#### Scenario: An artist's discography

- **WHEN** the artist's album group has sections Albums (3) and Singles (2)
- **THEN** the page shows an Albums rail with three cards and a Singles rail with two, each heading
  showing its count

### Requirement: An audio Entity page offers Radio

The web Entity page of an audio Entity SHALL offer a Radio action that reads `stationGenerate` with
the generic `entity` seed kind and the Entity's id and plays the window as the client queue.

#### Scenario: Artist radio

- **WHEN** the listener presses Radio on an artist page
- **THEN** the player opens on the first Station candidate with the window as its queue
