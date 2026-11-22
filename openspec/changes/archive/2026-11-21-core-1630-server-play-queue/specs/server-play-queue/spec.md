## ADDED Requirements

### Requirement: Play Queue entries have stable identity

The server SHALL store a unique ID per entry and SHALL preserve existing queue order and added timestamps when assigning IDs during migration. The server SHALL allow multiple entries for the same Media. The entry-addressed Graph mutations SHALL move, remove or move next exactly the visible entry identified by its ID. The legacy Media-addressed mutations SHALL remain available and act on the first visible matching entry in playback order.

#### Scenario: Removing one occurrence of a Media

- **WHEN** a Profile queues the same Media twice and removes the second entry by ID
- **THEN** the first entry remains, with its original ID

### Requirement: Clients can insert an ordered batch

A Profile SHALL be able to add a nonempty ordered list of visible Media IDs to the end of its Play Queue or immediately after the visible current entry. With no visible current entry, next placement SHALL put the batch first in the visible order. The server SHALL reject the entire batch if any Media is invalid or outside the caller's scope. Moving an existing entry next SHALL use the same placement rule.

#### Scenario: Adding the next entries in order

- **WHEN** a caller adds multiple Media IDs next while an entry is current
- **THEN** the new entries appear immediately after the current entry in the submitted order

### Requirement: Playback state is shared by the Profile

The server SHALL persist the current entry, its nonnegative position in milliseconds, shuffle and its playback order, and repeat mode (off, all or one). Clearing or deleting the current entry SHALL reset its position. Hidden entries and their playback position SHALL be filtered from responses using the caller's Media visibility. Queue writes SHALL support the existing optional expected Profile precondition.

#### Scenario: Resuming from a second device

- **WHEN** a device sets the current entry and playback position
- **THEN** another device bound to the same Profile with visibility of that entry reads that entry and position

#### Scenario: Turning shuffle on and off

- **WHEN** shuffle is enabled
- **THEN** devices read the same persisted shuffled order and the current entry and position stay the same
- **AND** disabling shuffle restores the underlying order of surviving entries
