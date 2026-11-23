## ADDED Requirements

### Requirement: Player Profile controllers list

A `player` Profile SHALL store `allAdults` (default `true`), an explicit `include` list, and an
explicit `exclude` list, evaluated live from each caller Profile's current kind (`include`, or
`allAdults` with kind `adult`; never when in `exclude`; `player` Profiles never). Only the owner
capability MAY read or update this policy via `playerControllers` and `setPlayerControllers`.

#### Scenario: Live membership evaluation across kind changes and explicit lists

- **WHEN** `allAdults` is `true`, a newly created `adult` Profile is evaluated against the list, and
  an `adult` Profile not in `include` is changed to `managed` or added to `exclude`
- **THEN** the new `adult` Profile is a controller, while the demoted or excluded Profile immediately
  ceases to be a controller

### Requirement: Non-leaking cross-Profile control and commit re-check

When a control target belongs to a `player` Profile, the server SHALL authorize controllers via
`IsPlayerController`, return `"unknown session"` (`NOT_FOUND`) to non-controllers across every
outcome (including nonexistent, unbound, offline, stale `expectedRevision`, and replayed/reused
`commandID`), re-check binding generation at commit, and rate-limit per controller (~30 per 10 s).

#### Scenario: Non-controller receives unknown session on stale revision or replay

- **WHEN** a non-controller sends `controlRendererSession` targeting a `player` Session with a stale
  revision or a reused `commandID`
- **THEN** the server refuses with `"unknown session"` (`NOT_FOUND`)

#### Scenario: Legacy controlSession refuses every player target

- **WHEN** any caller, including a listed controller, sends the legacy `controlSession` targeting a
  `player` Session
- **THEN** the server refuses with `"unknown session"` (`NOT_FOUND`), and only
  `controlRendererSession` may drive the display (ADR-0240 D3)

### Requirement: Dual-Profile verb authorization

For authorized controllers, `play-media` SHALL require both the caller's `PlaybackFilter()` and the
Player Profile's `PlaybackFilter()` to admit the Media; `stop` and `display-message` SHALL require
only controller membership; transport and skip verbs SHALL require caller `LibraryFilter()` on the
current item; and `resume` SHALL additionally require caller `PlaybackFilter()` on the current item.

#### Scenario: Dual-Profile playback authorization for play-media

- **WHEN** a `managed` controller sends `play-media` for an item it can see under `LibraryFilter()`
  but cannot play under `PlaybackFilter()`, or when an `adult` controller sends `play-media` for an
  item the Player Profile cannot play
- **THEN** the command is refused

### Requirement: Field-minimal observation and display presence

Controller reads (`playerDisplays`, `rendererSessionSnapshot`, conflict snapshots, and SSE
`session.control` delivery) SHALL expose identifying item fields only when visible under the
controller's `LibraryFilter()`, showing `"Private item"` with `positionSeconds` and
`durationSeconds` otherwise. Display presence SHALL track `ready`, `playing`, and `offline` (after
60 s without an SSE heartbeat) with stored `lastSeenAt`, returning `"display offline"` to
controllers when offline.

#### Scenario: Private item suppression and 60-second heartbeat expiry

- **WHEN** a display plays an item outside a controller's `LibraryFilter()` and later goes 60
  seconds without an SSE heartbeat
- **THEN** `playerDisplays` and conflict snapshots show `"Private item"` with `positionSeconds` and
  `durationSeconds` only, and controller commands after expiry fail with `"display offline"`
