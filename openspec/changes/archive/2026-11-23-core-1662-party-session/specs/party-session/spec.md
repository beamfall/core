## ADDED Requirements

### Requirement: A host Profile runs one live party session

The server SHALL let a signed-in Profile start a party session naming the Libraries guests may
search, with a fairness policy (round-robin or FIFO, default round-robin) and an auto-add setting
(default off). A Profile SHALL have at most one live session; starting another while one is live
SHALL return the live session unchanged.

#### Scenario: Starting twice returns the live session

- **WHEN** a host starts a session and then starts another
- **THEN** both calls return the same session ID and join code

### Requirement: The host controls and ends the session

The host SHALL be able to change fairness, auto-add and whether joining is open, and to end the
session, which revokes every guest token. A session SHALL end on its own 12 hours after it started.

#### Scenario: Ending revokes guests

- **WHEN** the host ends the session
- **THEN** every guest token of that session is refused

### Requirement: Join codes are short, rotatable and never confirm existence

A session SHALL have a four character join code drawn from an alphabet without ambiguous
characters (no 0, O, 1, I or L), unique among live sessions. The host SHALL be able to rotate it;
the old code then stops admitting guests. A join with an unknown code, a code of an ended session,
or a code of a session whose joining is closed SHALL fail with the same response. Join attempts
SHALL be rate-limited per source address and per code.

#### Scenario: Rotated code stops working

- **WHEN** the host rotates the join code
- **THEN** a guest joining with the old code receives the same failure as for an unknown code

### Requirement: Guests hold a scoped expiring capability

Joining with a valid code and a nonempty name SHALL create a guest participant and return a guest
token that the server stores only as a hash. The token SHALL authorize only: reading the session's
current entry and rotation, searching the session's Libraries within the host's visibility, and
submitting or cancelling the guest's own requests, and SHALL NOT authorize playback, streaming,
download or any Graph operation. A session SHALL admit at most 50 guests.

#### Scenario: Guest token cannot stream

- **WHEN** a guest token is presented to a streaming or Graph endpoint
- **THEN** the request is refused as unauthenticated

### Requirement: Guest tokens expire and can be revoked

A guest token SHALL stop working when the session ends, 12 hours after it was issued, or when the
host removes the guest.

#### Scenario: Removed guest is refused

- **WHEN** the host removes a guest
- **THEN** that guest's token is refused on every guest route

#### Scenario: Guest search stays inside the session Libraries

- **WHEN** a guest searches
- **THEN** results include only Media in the session's Libraries that the host can see

### Requirement: Requests honour approval and limits

A guest request SHALL be pending until the host approves it unless auto-add is on, in which case
it SHALL be waiting at once. A guest SHALL have at most three pending or waiting entries and SHALL
be rate-limited between requests. The host SHALL be able to approve or decline a pending request,
add an entry for any participant (creating a named participant on first use), remove an entry and
restore it, and remove a participant.

#### Scenario: Fourth request is refused

- **WHEN** a guest with three waiting entries submits another request
- **THEN** the request is refused and the guest's entries are unchanged

### Requirement: A request carries the singer's key

A guest request SHALL carry a key shift in whole semitones from -12 to +12, defaulting to 0, and
the server SHALL refuse any other value. The key SHALL be stored on the entry and shown on the
guest's own entries and in the host's rotation. When the entry becomes current, the shared
playback key SHALL start at the entry's key; the host SHALL still be able to change it live.

#### Scenario: A requested key applies when the turn starts

- **WHEN** a guest requests an entry with key -2 and the host advances to it
- **THEN** the shared playback anchor for that entry has key -2

#### Scenario: Out-of-range key is refused

- **WHEN** a guest requests an entry with key 13
- **THEN** the request is refused and no entry is created

### Requirement: The server orders the rotation

Under round-robin the next waiting entry SHALL be the oldest waiting entry of the participant who
has gone longest without a turn (a participant who never had a turn ranks by their first entry
time). Under FIFO it SHALL be the oldest waiting entry. When the host moves an entry it SHALL be
pinned at that position and the policy SHALL place the other entries around pins.

#### Scenario: Round-robin interleaves participants

- **WHEN** participant A adds three entries and then participant B adds one
- **THEN** the waiting order is A, B, A, A

#### Scenario: A pinned entry keeps its place

- **WHEN** the host moves an entry to the first waiting position
- **THEN** it stays first as other participants add entries

### Requirement: The server computes ETAs

Each waiting entry's ETA SHALL be the remaining time of the current entry plus the durations of the
entries ahead plus a changeover allowance per changeover. The allowance SHALL be the median observed
gap between entries in the session, 20 seconds before any is observed, clamped to 0–120 seconds. A
missing duration SHALL count as the session's median duration.

#### Scenario: Default changeover

- **WHEN** nothing has played yet and two 180-second entries wait behind a current entry with 60
  seconds left
- **THEN** their ETAs are 80 and 280 seconds

### Requirement: Playback state is shared through an anchor

The host SHALL set the current entry's playback state (playing or paused), position in song
milliseconds, tempo and key; the server SHALL stamp it with its own clock as an anchor. Advancing
SHALL mark the current entry done and make the next waiting entry current at tempo 1 and the
entry's requested key, and SHALL be idempotent
when it names the entry it advances from. Entries whose Media is no longer visible SHALL be
skipped and reported.

#### Scenario: Two screens advance at once

- **WHEN** two screens both advance from the same entry
- **THEN** the rotation moves forward exactly once

### Requirement: Stage screens follow the anchor and report offset

A signed-in household client or the host's pop-out window SHALL be able to register as a stage
screen of a session, read the current entry, playback anchor, up-next entries and the server clock,
and report its measured offset and round-trip time. Each screen SHALL carry a ticker flag stored on
the server, off by default for the lead screen and on for others, which the host can change, and
the host SHALL be able to drop a screen. A screen SHALL report its carrier as local or relay.

#### Scenario: Ticker flag survives a reload

- **WHEN** the host turns the ticker off for a screen and that screen reloads
- **THEN** the screen reads its ticker as off
