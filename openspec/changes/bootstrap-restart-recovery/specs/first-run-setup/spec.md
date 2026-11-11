## ADDED Requirements

### Requirement: First-run setup commits atomically

A successful first-run setup SHALL commit the owner Profile, its owner designation, the first
Manager Device and that Device's Session in one store transaction. A failure at any of those steps
SHALL leave none of them stored.

#### Scenario: Enrollment fails after the Profile is written

- **WHEN** the first Manager Device or its Session cannot be written during first-run setup
- **THEN** no new Profile, owner designation, Device or Session is stored, and the setup token
  stays valid for a retry in the same process

### Requirement: Setup token is issued until setup completes

At startup the server SHALL install a first-run setup token unless first-run setup is complete. Setup
SHALL count as complete only when an adult Profile exists and at least one Device has been
enrolled. Once complete, the server SHALL NOT install a setup token on any later start.

#### Scenario: Restart after a partial owner write

- **WHEN** the store holds an adult Profile but no Device, because an earlier setup stopped after
  writing the Profile, and the server starts
- **THEN** a setup token is installed and first-run setup can be completed through the normal setup
  route without editing the database

#### Scenario: Restart after successful setup

- **WHEN** first-run setup has succeeded and the server starts again
- **THEN** no setup token is installed and setup requests are refused

### Requirement: Setup recovery reuses the partial owner

When first-run setup completes on a store that holds Profiles but no Device, the server SHALL reuse
the designated owner Profile, or the oldest adult Profile when none is designated, as the owner
instead of creating another Profile. The store SHALL end with exactly one owner Profile, and the new
Manager Device's Session SHALL be bound to it.

#### Scenario: Setup after a partial owner write

- **WHEN** setup completes on a store holding an owner Profile and no Device
- **THEN** the store holds the same number of Profiles as before, that Profile is the only owner,
  and exactly one Device, with the Manager role, is enrolled for it
