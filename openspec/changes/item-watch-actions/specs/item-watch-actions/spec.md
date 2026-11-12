## ADDED Requirements

### Requirement: A Profile can mark an item watched or unwatched

The `setWatched` mutation SHALL set the calling Profile's watched state for one visible Media, or
for every visible Media a visible Entity relates to directly or through its same-Kind child
Entities. Marking watched SHALL clear the resume position and count one play when the Media was not
already watched. Marking unwatched SHALL clear the watched state, the resume position and the play
count. The mark SHALL be appended to the engagement event log so that a projection rebuild
reproduces it. A hidden or unknown target SHALL be refused the same way as a missing one, and no
other Profile's state SHALL change.

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

#### Scenario: Hidden target

- **WHEN** a Profile calls `setWatched` for a Media in a Library it cannot see
- **THEN** the call is refused as not found and no state is written

#### Scenario: Rebuild

- **WHEN** the playback projection is rebuilt from the engagement event log after manual marks
- **THEN** every Media has the same watched state and play count as before the rebuild

### Requirement: A Profile has a watchlist

The `setWatchlisted` mutation SHALL add one visible Media or Entity to the calling Profile's
watchlist, or remove it. Adding an item already listed SHALL keep it listed once. The `watchlist`
Source SHALL return the Profile's listed items that are visible under the session's filter, most
recently added first, and the default Home layout SHALL include a Watchlist shelf bound to it.

#### Scenario: Add and remove

- **WHEN** a Profile adds a Media and an Entity to its watchlist and then removes the Media
- **THEN** the `watchlist` Source returns only the Entity, and another Profile's watchlist is empty

#### Scenario: Item hidden after it was listed

- **WHEN** a listed Media's Library stops being visible to the session
- **THEN** the `watchlist` Source no longer returns it

### Requirement: A Profile can rate an item

The `setRating` mutation SHALL record a whole-number rating from 1 to 10 by the calling Profile for
one visible Media or Entity, and a rating of 0 SHALL clear it. Any other value SHALL be refused.
The `rated` Source SHALL return the Profile's rated visible items, highest rating first.

#### Scenario: Set, change and clear

- **WHEN** a Profile rates a Media 8, then 6, then 0
- **THEN** its state reads 8, then 6, then unrated, and the `rated` Source no longer returns it

#### Scenario: Out of range

- **WHEN** a Profile calls `setRating` with 11
- **THEN** the call is refused and the stored rating is unchanged

### Requirement: Item state is readable

The `itemState` field SHALL return, for one visible Media or Entity and the calling Profile, whether
it is watched, whether it is on the watchlist, and its rating (0 when unrated). An Entity SHALL read
as watched only when it relates to at least one visible Media and all of them are watched.

#### Scenario: Partly watched container

- **WHEN** one of an Entity's two Media is watched
- **THEN** the Entity's `itemState` reads `watched: false`

### Requirement: Changes reach the Profile's other sessions

Each successful `setWatched`, `setWatchlisted` or `setRating` call SHALL append one
`client.invalidate` event with reason `engagement-changed` for the calling Profile. The event SHALL
carry no Media or Entity identity and SHALL be delivered only to sessions of the same Profile. The
web viewer SHALL refresh its shelves and item state when it receives one.

#### Scenario: Second session

- **WHEN** a Profile marks a Media watched on one session while a second session of the same
  Profile is subscribed to the event stream
- **THEN** the second session receives a `client.invalidate` event with reason `engagement-changed`
  and a session of a different Profile receives nothing
