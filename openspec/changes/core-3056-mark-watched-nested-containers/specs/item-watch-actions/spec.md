## MODIFIED Requirements

### Requirement: A Profile can mark an item watched or unwatched

The `setWatched` mutation SHALL set the calling Profile's watched state for one visible Media, or
for every visible Media a visible Entity relates to directly or through its child Entities: a
same-Kind child under a declared parent relation, or a child of another container Kind in the same
Library that nests in it by a relation the Library's plugin declares between the two container
Kinds. Marking
watched SHALL clear the resume position and count one play when the Media was not already watched.
Marking unwatched SHALL clear the watched state, the resume position and the play count. The mark
SHALL survive a projection rebuild and SHALL NOT change another Profile's state.

#### Scenario: Mark one Media watched

- **WHEN** a Profile with no progress on a Media calls `setWatched` with `watched: true`
- **THEN** the Media reads as watched for that Profile with one play and no resume position, and
  still reads as unwatched for every other Profile

#### Scenario: Mark one Media unwatched

- **WHEN** a Profile that has watched a Media calls `setWatched` with `watched: false`
- **THEN** the Media reads as unwatched for that Profile and leaves the continue-watching and
  recently-played Sources

#### Scenario: Mark a container watched

- **WHEN** a Profile calls `setWatched` with an Entity that relates to two Media and has a
  same-Kind child Entity relating to a third
- **THEN** all three Media read as watched and the Entity's state reads as watched

#### Scenario: Mark a container of nested containers watched

- **WHEN** a Profile calls `setWatched` with `watched: true` on a container Entity holding no Media
  of its own, whose parts (another container Kind) nest in it by a declared relation and hold three
  Media between them
- **THEN** all three Media read as watched and the Entity's state reads as watched, and a part
  linked to it only by an undeclared relation, or by a relation only another plugin declares, keeps
  its Media unwatched

#### Scenario: Mark a container of nested containers unwatched

- **WHEN** the same Profile then calls `setWatched` with `watched: false` on that Entity
- **THEN** all three Media read as unwatched

#### Scenario: Hidden target

- **WHEN** a Profile calls `setWatched` for a Media in a Library it cannot see
- **THEN** the call is refused as not found and no state is written

#### Scenario: Rebuild

- **WHEN** the playback projection is rebuilt from the engagement event log after manual marks
- **THEN** every Media has the same watched state and play count as before the rebuild
