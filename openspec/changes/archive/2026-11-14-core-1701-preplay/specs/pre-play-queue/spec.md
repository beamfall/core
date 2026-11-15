## ADDED Requirements

### Requirement: Owner pre-play clip list

The server SHALL store an ordered, de-duplicated list of at most 10 Media IDs as the Owner's
pre-play clips. Only an Owner SHALL read or replace it, and every ID SHALL name an existing,
untrashed Media.

#### Scenario: Owner sets clips

- **WHEN** an Owner calls `setPrePlayClips` with two Media IDs, one repeated
- **THEN** the stored list holds the two IDs once each, in first-seen order

#### Scenario: Unknown clip rejected

- **WHEN** an Owner names a Media ID that does not exist
- **THEN** the mutation fails with a validation error and the stored list is unchanged

#### Scenario: Non-Owner refused

- **WHEN** a non-Owner session calls `setPrePlayClips` or `prePlayClips`
- **THEN** the call is refused

### Requirement: Pre-play queue is decided by an active plugin policy

`prePlayQueue(mediaID)` SHALL return an empty list unless the Profile has enabled pre-play, the
feature is visible to the caller, and an active plugin policy reports the feature eligible. When
eligible it SHALL return the Owner's clips, then the policy's suggested items, each visible to the
caller, de-duplicated, never including the feature itself.

#### Scenario: Profile has not enabled pre-play

- **WHEN** a Profile without `preplay.enabled=on` asks for a feature's queue
- **THEN** the queue is empty

#### Scenario: Eligible feature

- **WHEN** an enabled Profile asks for an eligible feature's queue
- **THEN** the Owner's visible clips come first with role `clip`, then the suggested items with
  role `suggested` and the policy's label

#### Scenario: Hidden item dropped

- **WHEN** a clip or a suggested item sits in a Library the Profile cannot see
- **THEN** it is absent from that Profile's queue

#### Scenario: No policy

- **WHEN** no active plugin contributes a pre-play policy
- **THEN** every queue is empty

### Requirement: Clients play the queue before the feature with skip

The web player SHALL play a non-empty pre-play queue in order before the feature, offering a
control to skip the current item and one to skip the rest, and SHALL NOT record watch progress
for pre-play items.

#### Scenario: Skip all

- **WHEN** the viewer chooses "Skip all" during a pre-play item
- **THEN** the feature starts at once

#### Scenario: Item needing a transcode

- **WHEN** a pre-play item's playback decision is a server transcode rather than direct play
- **THEN** the web player passes over it, so pre-play never holds one of the Profile's concurrent
  streams that the feature then needs

#### Scenario: Autoplay refused

- **WHEN** the browser refuses to start a pre-play item without a user gesture
- **THEN** the player shows playback controls for that item instead of stalling
