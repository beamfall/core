## ADDED Requirements

### Requirement: A lead party follows the lead's audible position

A Watch Party formed with `lead` SHALL have a lead (its host session) and followers (every other
member). The server SHALL never correct the lead. When the lead reports a position more than
40 ms from the shared timeline at the sample instant, the server SHALL re-anchor the timeline on
the report and send every member Profile a `transition` event with `lead` set. Only the lead's
report SHALL be accepted, and a paused timeline SHALL NOT be moved by a report.

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

### Requirement: A lead party's timeline carries the lead's speed

A lead party's `transition` events SHALL carry `ratePermille`, the lead's speed in thousandths of
normal speed, and the shared timeline SHALL advance at that speed. A lead party's transition SHALL
state the position as of the instant it is sent, with no pre-buffer window.

#### Scenario: The lead changes speed

- **WHEN** the lead reports that it now plays at 1250 thousandths
- **THEN** every follower receives a `transition` with `ratePermille` 1250 and the timeline
  advances 1.25 seconds per second

### Requirement: Only the lead moves a lead party

A lead party SHALL never be relaxed. A follower's transport SHALL be refused without creating a
request. When the lead leaves or its stream expires, the party SHALL end for every member with an
`ended` event, and the host role SHALL NOT pass to a follower.

#### Scenario: A follower seeks

- **WHEN** a follower sends transport to a lead party
- **THEN** it is refused, no request reaches the lead and the timeline is unchanged

#### Scenario: The lead leaves

- **WHEN** the lead leaves a lead party that has followers
- **THEN** every follower receives `ended` and no follower becomes host

### Requirement: Followers find and rejoin a lead party

An invite to a lead party SHALL remain valid after it is accepted, and the lead's own Profile
SHALL be able to join further sessions without an invite. While the lead reports, the server SHALL
re-state the timeline to every member Profile at least every 5 seconds.

#### Scenario: A follower screen reloads

- **WHEN** a follower's page reloads while the lead party is running
- **THEN** it joins again without a new invite and is on the timeline within 5 seconds

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
