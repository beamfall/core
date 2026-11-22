## ADDED Requirements

### Requirement: Unplayed children hide their still and summary

The web viewer SHALL draw a neutral placeholder instead of the still of an unplayed video Media item a container holds, and SHALL hide its summary, unless the Profile turned the guard off or revealed the item or its container. Titles SHALL stay visible. An item with any progress SHALL show normally.

#### Scenario: Unplayed child in a container's rows

- **WHEN** a guarded Profile opens a container whose rows include an unplayed video item with a still and a summary
- **THEN** the row shows a placeholder instead of the still and no summary text, and its title is shown

#### Scenario: Played or in-progress child

- **WHEN** the item is completed or has a position above zero
- **THEN** its still and summary show

#### Scenario: Audio child

- **WHEN** the unplayed child's media class is audio
- **THEN** its artwork and summary show

#### Scenario: Next up card

- **WHEN** a Home row shows an unplayed guarded child whose card art would be its still
- **THEN** the card shows the placeholder instead

### Requirement: A Profile setting turns the guard off

Profile Settings SHALL offer "Hide details of unwatched items", on by default, stored as the client pref `profile.show-unplayed-details`, where `"on"` turns the guard off and an absent key keeps it on.

#### Scenario: Turning the guard off

- **WHEN** the Profile switches the setting off
- **THEN** `profile.show-unplayed-details` is set to `"on"` and unplayed children show their stills and summaries

#### Scenario: Default

- **WHEN** a Profile has never changed the setting
- **THEN** the guard is on

### Requirement: A Profile can reveal one item or a whole container

A guarded item SHALL offer a keyboard-reachable, labelled reveal control and a More-menu reveal that show it until the page is reloaded. A container's rows SHALL offer a toggle that reveals every child of that container and persists for the Profile as the client pref `profile.show-unplayed-details.container.<id>`.

#### Scenario: Per-item reveal is session-only

- **WHEN** the Profile reveals one item and then reloads the page
- **THEN** the item is revealed until the reload and guarded after it, and no client pref is written

#### Scenario: Per-container reveal persists

- **WHEN** the Profile turns on Show spoilers for a container
- **THEN** the container's pref is set to `"on"`, its children show, and they still show after a reload

#### Scenario: Reduced motion

- **WHEN** the browser prefers reduced motion
- **THEN** revealing an item changes it without a transition
