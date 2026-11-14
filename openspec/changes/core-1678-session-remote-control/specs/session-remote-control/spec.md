## ADDED Requirements

### Requirement: Closed session-control vocabulary with typed arguments

The server SHALL accept exactly the session-control verbs `pause`, `resume`, `stop`, `skip-media`,
`skip-track`, `seek`, `set-volume`, `mute`, `unmute`, `select-audio-track`,
`select-subtitle-track`, `play-media` and `display-message`. Each verb SHALL take only its own
arguments, and the server SHALL reject a missing, malformed, out-of-range or misplaced argument as a
client error without writing an outbox event.

#### Scenario: Seek carries a position

- **WHEN** a same-Profile device sends `seek` with `positionMs` 90000 to a session with an active
  playback
- **THEN** a `session.control` event addressed to that session carries `command` `seek` and
  `positionMs` 90000

#### Scenario: Skip defaults to next

- **WHEN** a device sends `skip-track` with no direction
- **THEN** the event carries `direction` `next`

#### Scenario: Subtitle track off

- **WHEN** a device sends `select-subtitle-track` with no track
- **THEN** the event carries an empty `trackID`, meaning subtitles off

#### Scenario: Misplaced argument

- **WHEN** a device sends `pause` with a `positionMs`
- **THEN** the request fails with "pause does not take that argument" and no event is written

#### Scenario: Volume out of range

- **WHEN** a device sends `set-volume` with volume 101
- **THEN** the request fails and no event is written

#### Scenario: Message with control characters

- **WHEN** a device sends `display-message` with text containing a control character, blank text or
  more than 256 characters
- **THEN** the request fails and no event is written

### Requirement: Session control is authorized per Profile and reveal

The server SHALL publish a session-control command only when the target session belongs to the
caller's Profile and does not play Media the caller cannot reveal. A cross-Profile or unknown target
SHALL be reported as "unknown session". Verbs other than `play-media` SHALL require an active
playback on the target.

#### Scenario: Cross-Profile target

- **WHEN** a device in another Profile sends any verb to a session
- **THEN** the request fails with "unknown session" and no event is written

#### Scenario: Unrevealed caller

- **WHEN** an unrevealed device sends any verb to a same-Profile session playing restricted Media
- **THEN** the request fails with "session not controllable" without naming the Media

#### Scenario: Mixed visibility

- **WHEN** a session has playback rows for both visible and restricted Media and an unrevealed
  same-Profile device sends any verb to it
- **THEN** the request fails with "session not controllable", whichever row is listed first

### Requirement: Play on another device

`play-media` SHALL start Media on a live device session in the caller's Profile, whether or not it
is playing. The Media SHALL be visible to the caller and SHALL be the event's reveal anchor. An
unknown Media and a Media hidden from the caller SHALL both fail with "unknown media".

#### Scenario: Idle target

- **WHEN** a device sends `play-media` with a visible `mediaID` and `positionMs` 5000 to an idle
  same-Profile device session
- **THEN** an event addressed to that session carries `command` `play-media`, the `mediaID` and
  `positionMs` 5000

#### Scenario: Hidden Media

- **WHEN** an unrevealed device sends `play-media` with a restricted Media's id
- **THEN** the request fails with "unknown media", the same as for an id that does not exist

### Requirement: Fixed /v1 body for playback control

`POST /v1/playback-control` SHALL keep its request body of `sessionID` and `command`. It SHALL
accept the verbs that need no argument and SHALL answer 400 naming the missing argument for the
others.

#### Scenario: Seek over /v1

- **WHEN** an integration key posts `seek` to `/v1/playback-control`
- **THEN** the response is 400 "seek requires positionMs"

### Requirement: Web remote and receiver

The web app SHALL list the Profile's other active sessions with transport controls, SHALL offer
"Play on" for an item to the Profile's other device sessions, and SHALL apply `session.control`
events addressed to its own session to the player it is running. The receiver SHALL apply each
event at most once, SHALL refuse a transport command whose Media is not the one it is playing, and
SHALL end playback on a remote `stop` without returning to an earlier player.

#### Scenario: Pause from another tab

- **WHEN** a second browser in the same Profile sends `pause` to a web session that is playing
- **THEN** that web player pauses

#### Scenario: Player moved on

- **WHEN** a `pause` authorized for one Media reaches a web player now playing another
- **THEN** the player ignores it

#### Scenario: Viewer remounted

- **WHEN** a web viewer remounts and is sent a `play-media` event it already applied
- **THEN** it does not start playback again
