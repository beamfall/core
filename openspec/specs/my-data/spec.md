# my-data Specification

## Purpose
Lists a Profile's outstanding Stream Tokens until they expire, including tokens whose playback has
ended. Keeps that record separate from live sessions so ending playback removes it from active-session
views without hiding a still-valid grant.

## Requirements

### Requirement: My Data lists stream tokens until they expire

My Data's stream-token list SHALL include every Stream Token issued to the Profile that has not
expired, including a token whose playback the player has ended with `endPlayback`. Ending a
playback SHALL NOT bring it back onto `liveSessions` or the active-session count; only a new grant
for the item SHALL.

#### Scenario: Player closes

- **WHEN** a device calls `endPlayback` for an item it holds an unexpired Stream Token for
- **THEN** My Data still lists that token
- **AND** `liveSessions` and the active-session count no longer include the playback

#### Scenario: Late progress report

- **WHEN** the device reports progress for the item after `endPlayback`
- **THEN** `liveSessions` still does not list the playback

#### Scenario: Playing again

- **WHEN** the device starts the item again and is issued a new grant
- **THEN** `liveSessions` lists the playback again

#### Scenario: Token expires

- **WHEN** the ended playback's token expires
- **THEN** My Data no longer lists it

### Requirement: Viewers read their own watch history and stats

The session fields `myPlayHistory` and `myPlayStats` SHALL return the same plays and breakdowns as
`playHistory` and `playStats`, limited to the signed-in profile, with no profile argument. They
SHALL drop plays of items the profile cannot see. A session with no profile SHALL be refused.

#### Scenario: Viewer sees only their own plays

- **WHEN** two profiles have plays and one of them reads `myPlayHistory`
- **THEN** only that profile's plays are returned

#### Scenario: Hidden item

- **WHEN** a profile played an item in a Library it can no longer see
- **THEN** `myPlayHistory` does not list that play and `myPlayStats` does not count it
