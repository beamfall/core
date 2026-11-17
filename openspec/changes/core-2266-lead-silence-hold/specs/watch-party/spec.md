## MODIFIED Requirements

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

## ADDED Requirements

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
