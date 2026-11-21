## ADDED Requirements

### Requirement: A Profile can remove an item from Continue Watching

The `setContinueWatchingHidden` mutation SHALL, with `hidden: true`, remove one visible Media from
the calling Profile's `continue-watching` Source without changing its resume position, watched state
or play count, and SHALL, with `hidden: false`, return it in the place its last playback gives it.
A Media the Profile has no progress on SHALL be accepted with nothing written. The removal SHALL
survive a projection rebuild and SHALL NOT change another Profile's Source.

#### Scenario: Remove keeps progress

- **WHEN** a Profile with a resume position on a Media calls `setContinueWatchingHidden` with
  `hidden: true`
- **THEN** the `continue-watching` Source no longer returns the Media, the Media keeps its resume
  position, and the `resume-progress` "in-progress" filter still returns it

#### Scenario: Undo

- **WHEN** the Profile then calls `setContinueWatchingHidden` with `hidden: false`
- **THEN** the `continue-watching` Source returns the same items in the same order as before the
  removal

#### Scenario: Hidden target

- **WHEN** a Profile calls `setContinueWatchingHidden` for a Media in a Library it cannot see
- **THEN** the call is refused as not found and no state is written

#### Scenario: Rebuild

- **WHEN** the playback projection is rebuilt from the engagement event log after a removal
- **THEN** the Media is still off the `continue-watching` Source with the same resume position

#### Scenario: Unlogged row

- **WHEN** a rebuild finds a progress row with no logged event that creates it before its first
  removal
- **THEN** the rebuild is refused and the row is left as it was

### Requirement: Continue Watching removal honours the expected Profile

`setContinueWatchingHidden` SHALL accept the optional `expectedProfileID` precondition and SHALL
refuse the write with `PROFILE_MISMATCH` when the session is no longer bound to that Profile.

#### Scenario: Stale Profile

- **WHEN** a client sends `expectedProfileID` for a Profile the session was switched away from
- **THEN** the call is refused with `PROFILE_MISMATCH` and no Profile's state changes

### Requirement: Playing a removed item returns it to Continue Watching

The next playback report for the Profile and a removed Media that moves the resume position SHALL
return the Media to the `continue-watching` Source. A report that repeats the stored position SHALL
NOT.

#### Scenario: Playing again returns the item

- **WHEN** a removed Media is played again and a playback report moves its resume position
- **THEN** the `continue-watching` Source returns the Media again

#### Scenario: Paused player

- **WHEN** a removed Media receives a playback report with the position it already had
- **THEN** the Media stays off the `continue-watching` Source
