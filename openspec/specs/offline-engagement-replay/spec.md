# offline-engagement-replay Specification

## Purpose
Let a client that played Media offline replay its progress and completion events to the server idempotently, merging them with online state and refreshing the user's other clients.

## Requirements

### Requirement: Offline engagement replay mutation

The server SHALL accept a batch of at most 500 offline engagement events through the
`replayOfflineEngagement` mutation for the Profile the session is bound to, honouring the optional
`expectedProfileID` precondition, and SHALL answer each event with a terminal status of `applied`,
`duplicate` or `rejected` plus per-status counts.

#### Scenario: A valid batch is applied

- **WHEN** a client replays two valid events for Media the Profile can see
- **THEN** both are answered `applied` and the Profile's playback progress reflects them

#### Scenario: Invisible Media is rejected

- **WHEN** an event names Media the Profile cannot see under its playback filter
- **THEN** that event is answered `rejected` with a reason and nothing is recorded for it

### Requirement: Replay is idempotent per device event

The server SHALL record every applied `(Profile, Device, eventID)` in the same transaction as its
effect and SHALL answer a repeated event `duplicate` without changing engagement.

#### Scenario: A batch replayed twice

- **WHEN** a client replays the same batch a second time
- **THEN** every event is answered `duplicate` and play counts, watched state and position are unchanged

#### Scenario: Events are Profile-scoped

- **WHEN** two Profiles replay events with the same event ID
- **THEN** each Profile's event is applied to that Profile only

### Requirement: Offline position and completion merge rules

The server SHALL apply an offline position only when its played-at time is at or after the stored
playback event time, clamping a future played-at time to the server clock; SHALL mark Media watched
for a completed event whatever its timestamp; SHALL never un-complete watched Media from an offline
event; and SHALL count a play only on the unwatched-to-watched transition.

#### Scenario: An older offline position

- **WHEN** an offline event's played-at time is older than the position recorded online
- **THEN** the online position is kept

#### Scenario: Offline completion

- **WHEN** a completed offline event is replayed for unwatched Media, even with an older timestamp
- **THEN** the Media is watched, its resume point is cleared and its play count rises by one

#### Scenario: Partial play of watched Media

- **WHEN** a partial offline event is replayed for Media already watched
- **THEN** the Media stays watched and its play count is unchanged

### Requirement: Replay refreshes other clients

The server SHALL raise one `engagement-changed` invalidation for the Profile after a batch in which
any event was applied.

#### Scenario: Other clients refresh

- **WHEN** a batch applies at least one event
- **THEN** one engagement-changed invalidation is appended for the Profile
