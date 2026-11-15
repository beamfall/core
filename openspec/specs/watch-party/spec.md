# watch-party Specification

## Purpose
Lets viewers in different Profiles watch one Media together: a host's play, pause and seek reach every accepted member, and members can request transport changes, from the web player as well as the server API.

## Requirements

### Requirement: Watch Party sync reaches every member Profile

Every Watch Party `group.sync` event SHALL be delivered to each member's event stream, whatever
the member's Profile. The server SHALL write one event per distinct member Profile, the host's
included. The event-stream Profile gate SHALL stay unchanged.

#### Scenario: Cross-Profile member sees the host's seek

- **WHEN** a host in one Profile seeks and a member from another Profile has joined
- **THEN** the member's event stream receives a `transition` event for the party with the new
  position

### Requirement: Watch Party titles are visible to every member

A Watch Party SHALL move to another Media only if every member's own session can see it. A move to
a Media hidden from any member SHALL be refused, and no `group.sync` event SHALL be written for it.
An invitee SHALL be able to accept only if their session can see both the party's content and the
Media it is on now. Each party's transitions, accepts and rosters SHALL reach every member in the
same order.

#### Scenario: Host picks a title a member cannot see

- **WHEN** the host moves the party to a Media in a Library hidden from another member's Profile
- **THEN** the move is refused, the party stays on its current Media and no member receives a
  `transition` event

#### Scenario: Invitee cannot see the title now playing

- **WHEN** the host has moved the party to a Media hidden from an invitee's Profile and the
  invitee accepts
- **THEN** the accept is refused and no `roster` event is written

### Requirement: Watch Party timeline can be paused

A Watch Party SHALL carry a paused state. It starts paused at position zero on its content. A
host transition SHALL set the item, the position and the paused state. While the party is paused,
its anchor position SHALL NOT advance, and transition and roster events SHALL carry `paused`.

#### Scenario: Late joiner to a paused party

- **WHEN** the host pauses at 90 seconds and a member accepts a minute later
- **THEN** the accept result reports position 90 seconds

### Requirement: Watch Party roster is broadcast

The server SHALL emit a `roster` signal to every member Profile when a party is created, when a
member accepts and when a member leaves. It carries the members' display names and the current
anchor (item, position, present-at and paused).

#### Scenario: Member joins

- **WHEN** an invited member accepts
- **THEN** the host and every other member receive a `roster` event listing the new member

### Requirement: Watch Party is limited to the home network

Until CORE-0531's legal review promotes remote Watch Party, creating or joining a party SHALL be
refused for requests that don't come from the home network (relayed, forwarded without a
declared proxy, or from a public address). The refusal SHALL say that Watch Party is available
on the home network only. Invitations SHALL go only to a named Profile on the same server, and
there SHALL be no public or anonymous join link.

#### Scenario: Remote create is refused

- **WHEN** a signed-in session reaches the server through the relay and calls `createWatchParty`
- **THEN** the mutation fails with the home-network-only message and no party is formed

### Requirement: Web viewer manages a Watch Party

The web viewer SHALL let a signed-in user:

- start a Watch Party from the player;
- invite another Profile by name;
- join from the invite notification;
- see the members;
- leave.

#### Scenario: Join from notification

- **WHEN** an invited user selects Join on a Watch Party invite in the notifications inbox
- **THEN** the viewer accepts the invite, opens the player on the party's content at the party's
  position, and shows the member list

### Requirement: Web viewer follows the party timeline

While in a party, the web viewer SHALL follow play, pause and seek from the sync stream. It SHALL
correct its own drift: re-seek beyond 500 ms, adjust the playback rate slightly below that. It
SHALL show the current drift. A host's transport actions SHALL drive the party, and a member's
SHALL be sent as transport requests. A `roster` event that describes the timeline the viewer
already follows SHALL NOT interrupt its playback.

#### Scenario: Host pauses

- **WHEN** the host pauses
- **THEN** every member's player pauses at the host's position and shows its drift

### Requirement: Web viewer sends only the viewer's own transport

In a party, the web viewer SHALL send a play, pause or seek only when it follows the viewer's own
input: the in-page controls, the release of a scrubber drag (pointer or touch) or the OS media controls. The
browser's autoplay and the viewer's own drift corrections SHALL NOT be sent.

#### Scenario: Autoplay on joining a paused party

- **WHEN** a member joins a paused party and the browser starts playback on its own
- **THEN** the viewer pauses again and sends no transport

#### Scenario: Pause from the OS media controls

- **WHEN** the viewer pauses from the lock screen or a media key
- **THEN** the pause is sent to the party like the in-page Pause

### Requirement: Web viewer starts a Watch Party from the media detail page

The web media detail page SHALL offer a "Watch together" secondary action only when the server has accepted the tab's Watch Party clock calibration (the home network) and no party is running. Choosing it SHALL start the party through the same create path as the player, held at the viewer's start position, and SHALL open the player with the Watch Party invite panel open. A failed start SHALL leave the viewer on the page with a notice naming the cause.

#### Scenario: Off the home network
- **WHEN** the server refuses the clock calibration
- **THEN** the detail page does not show Watch together

#### Scenario: Starting from the detail page
- **GIVEN** the viewer is on the home network with a saved position of 42.5 seconds
- **WHEN** the viewer chooses Watch together
- **THEN** a party starts for the item at 42500 ms
- **AND** the player opens past pre-play with the Watch Party invite panel open

#### Scenario: The start fails
- **WHEN** the server refuses to create the party
- **THEN** the viewer stays on the detail page and sees a notice saying why
