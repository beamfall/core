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

### Requirement: A lead party follows the lead's audible position

A Watch Party formed with `lead` SHALL have a lead (its host session) and followers (every other
member). The server SHALL never correct the lead. When the lead reports a position more than
40 ms from the shared timeline at the sample instant, the server SHALL re-anchor the timeline on
the report and send every member Profile a `transition` event with `lead` set. Only the lead's
report SHALL be accepted, and a timeline the lead paused SHALL NOT be moved by a report.

#### Scenario: The lead stalls for two seconds

- **WHEN** the lead's player stalls for 2 seconds and then reports its position
- **THEN** the shared timeline moves back 2 seconds and every follower receives the new timeline
  in one `transition` event

#### Scenario: The lead is on time

- **WHEN** the lead reports a position within 40 ms of the shared timeline at the sample instant
- **THEN** the timeline is not moved, however late the report arrived

#### Scenario: A follower sends a lead report

- **WHEN** a follower sends a lead report
- **THEN** it is refused and the timeline is unchanged

### Requirement: A lead report that says nothing about now is dropped

A lead report SHALL be dropped when its sample is more than 5 seconds old, when its sample
precedes the instant the timeline was last anchored, or when the timeline was stated less than
250 ms earlier. A lead report without a position or a sample instant SHALL be refused.

#### Scenario: A report sampled before a seek arrives after it

- **WHEN** the lead seeks to 60 seconds and a report sampled before the seek arrives afterwards
- **THEN** the report is dropped and the timeline stays at the seek

#### Scenario: An on-time report arrives 8 seconds late

- **WHEN** a report that was on time when sampled arrives 8 seconds later
- **THEN** the timeline is not moved

### Requirement: A lead party's timeline carries the lead's speed

A lead party's `transition` events SHALL carry `ratePermille`, the lead's speed in thousandths of
normal speed, whenever that speed is not normal, and the shared timeline SHALL advance at that
speed. A lead party's transition SHALL
state the position as of the instant it is sent, with no pre-buffer window.

#### Scenario: The lead changes speed

- **WHEN** the lead reports that it now plays at 1250 thousandths
- **THEN** every follower receives a `transition` with `ratePermille` 1250 and the timeline
  advances 1.25 seconds per second

### Requirement: Only the lead moves a lead party

A lead party SHALL never be relaxed. A follower's transport SHALL be refused without creating a
request. When the lead leaves or its stream expires, the party SHALL end for every member with an
`ended` event, and the host role SHALL NOT pass to a follower. A Watch Party SHALL NOT be joined,
left or moved through the Ambient Group operations, whichever Profile the session is in.

#### Scenario: A follower seeks

- **WHEN** a follower sends transport to a lead party
- **THEN** it is refused, no request reaches the lead and the timeline is unchanged

#### Scenario: A follower in the lead's Profile uses the Ambient Group transition

- **WHEN** a follower session in the lead's own Profile sends an Ambient Group transition naming
  the party
- **THEN** it is refused as an unknown group and the timeline is unchanged

#### Scenario: The lead leaves

- **WHEN** the lead leaves a lead party that has followers
- **THEN** every follower receives `ended` and no follower becomes host

### Requirement: Followers find and rejoin a lead party

An invite to a lead party SHALL remain valid after it is accepted, and the lead's own Profile
SHALL be able to join further sessions without an invite. While the lead reports, the server SHALL
re-state the timeline to every member Profile at least every 5 seconds. A lead party's roster
SHALL carry `lead` and the timeline's speed, so a joining session knows it follows before it plays.

#### Scenario: A follower screen reloads

- **WHEN** a follower's page reloads while the lead party is running
- **THEN** it joins again without a new invite and is on the timeline within 5 seconds

#### Scenario: A screen joins a lead party

- **WHEN** a session accepts an invite to a lead party
- **THEN** the roster written by that accept is marked `lead`

### Requirement: Follower screens show the picture locked to the lead's audio

In the web player a follower SHALL be muted, SHALL follow the shared timeline at its speed, and
SHALL hold its picture within 40 ms of the timeline after applying that screen's display delay
trim. The lead's player SHALL play freely, SHALL NOT be re-seeked or rate-nudged by the party, and
SHALL report its position about once a second. The display delay trim SHALL be set per screen and
kept on that screen.

#### Scenario: A follower drifts

- **WHEN** a follower's picture is more than 40 ms from the timeline
- **THEN** the follower corrects itself and the lead's playback is untouched

#### Scenario: A screen has a slow panel

- **WHEN** a display delay trim of 80 ms is set on a follower screen
- **THEN** that screen shows each frame 80 ms earlier than an untrimmed screen and keeps the trim
  after a reload

### Requirement: The shared clock resolves better than a millisecond

The calibration exchange SHALL carry timestamps with sub-millisecond resolution. A client SHALL
estimate its clock offset from several exchanges, using the exchange with the lowest round trip,
and SHALL re-estimate while it is in a party.

#### Scenario: One exchange is delayed

- **WHEN** one of several calibration exchanges takes 80 ms longer than the others
- **THEN** the offset estimate comes from a faster exchange and is within 5 ms of the true offset

### Requirement: A lead party is held while its lead is silent

When a playing lead party's lead has sent no report and no transport for 3 seconds, the server
SHALL hold the timeline at the position it had when the lead was last heard and SHALL send every
member Profile a paused `transition` event. The lead's next report or transport SHALL resume the
timeline on that report or transport, with a `transition` event to every member Profile. A hold
SHALL NOT end the party and SHALL NOT pass the host role.

#### Scenario: The lead stops reporting

- **WHEN** the lead of a playing lead party sends nothing for 3 seconds
- **THEN** every follower receives a paused `transition` at the position the timeline had when
  the lead was last heard

#### Scenario: The lead reports again

- **WHEN** the lead of a held party reports that it is playing at 12 seconds
- **THEN** every follower receives a playing `transition` anchored on that report

#### Scenario: The lead paused the party itself

- **WHEN** the lead pauses the party and then sends nothing for 3 seconds
- **THEN** no hold is sent, and a later report does not move the timeline
