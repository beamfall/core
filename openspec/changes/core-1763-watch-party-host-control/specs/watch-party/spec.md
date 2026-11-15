## ADDED Requirements

### Requirement: Host leave hands the party off

When the host leaves a Watch Party that still has members, the host role SHALL pass to the
member who joined earliest among those still present. The server SHALL send every member Profile
a `roster` event with `handoff` set and the new host's display name. Only the new host's own
Profile row SHALL carry the new host's session id. Requests pending with the old host SHALL be
dropped. When the last member leaves, the party SHALL dissolve.

#### Scenario: Host leaves a party with one member

- **WHEN** the host leaves and one member remains
- **THEN** that member becomes host, its transport is authoritative, the old host's transport is
  refused, and every member receives a hand-off `roster` naming the new host

#### Scenario: Member learns who hosts now

- **WHEN** a hand-off `roster` reaches a member's web player
- **THEN** the player shows the new host's name, or "You're now hosting" on the new host's own
  player

### Requirement: Host can end the party for everyone

The host SHALL be able to end the party with `leaveWatchParty(end: true)`. The server SHALL then
remove every member, delete the party and send every member Profile an `ended` event with the
host's display name. A non-host's end request SHALL be refused and change nothing.

#### Scenario: Host ends the party

- **WHEN** the host chooses "End for everyone"
- **THEN** every member's player leaves the party, keeps playing on its own and shows that the
  host ended it

#### Scenario: Member tries to end the party

- **WHEN** a member who is not the host sends `leaveWatchParty` with `end: true`
- **THEN** the request is refused and the party is unchanged

### Requirement: Members whose stream dropped are expired

The server SHALL track open event streams per session. When a party member's last stream closes
and none reopens within 20 seconds, the server SHALL remove that member as if it had left,
handing the party off or dissolving it as a leave does.

#### Scenario: Member closes the laptop

- **WHEN** a member's only event stream closes and does not reopen within the grace
- **THEN** the member is removed from the party and the others receive a `roster` without it

### Requirement: Web viewer leaves when the page goes away

On `pagehide`, the web player SHALL leave its party with a keepalive request, so the leave
reaches the server even though the page is unloading. The server-side expiry SHALL remain the
backstop.

#### Scenario: Viewer closes the tab

- **WHEN** a viewer in a party closes the tab or navigates away
- **THEN** a keepalive `leaveWatchParty` request is sent and the party sees the leave within
  seconds

### Requirement: Watch Party requests use their own notification kind

In a host-only party, a member's play, pause or seek SHALL be stored as a pending request and
surfaced to the host's Profile as a `watch-party-request` notification. The notification's `ref`
SHALL be `<partyID>:<requestID>` and its body the requester's display name. The server SHALL also
send a `request` event to the host's Profile only. The timeline SHALL NOT move until the host
approves. In a relaxed party, a member's transport SHALL apply directly, as before.

#### Scenario: Member seeks in a host-only party

- **WHEN** a member seeks in a party started with host-only control
- **THEN** the host's Profile receives a `watch-party-request` notification and a `request` event,
  no `watch-party-invite` notification is written and no member receives a `transition`

### Requirement: Host approves or declines a request

The host SHALL approve a pending request with `watchPartyTransport(request: <id>)`, which applies
the stored transport and moves every member. The host SHALL decline with
`watchPartyTransport(request: <id>, decline: true)`, which drops the request and leaves the
timeline unchanged. Only the host SHALL decide, and an unknown or already decided request SHALL
be refused. The web viewer SHALL offer Approve and Decline in the player and on the notifications
page.

#### Scenario: Host approves from the notifications page

- **WHEN** the host selects Approve on a `watch-party-request` notification
- **THEN** the stored seek is applied and every member receives a `transition` to it

#### Scenario: Host declines from the player

- **WHEN** the host selects Decline on a pending request in the player
- **THEN** the request is dropped and no `transition` is sent

### Requirement: Start offers relaxed or host-only control

Starting a party from the web viewer SHALL let the host choose relaxed control, where anyone may
play, pause or seek, or host-only control, where members' transport becomes requests.

#### Scenario: Host starts a host-only party

- **WHEN** the host picks host-only control and starts the party
- **THEN** the party is created with `relaxed: false`

### Requirement: Web player reports its drift

While a party is playing, the web player SHALL report its measured drift with `reportDrift` at
most once every 5 seconds. The server SHALL accept a report from any party member, whatever its
Profile. It SHALL classify the drift and SHALL NOT re-anchor the party for one member. A nudge
SHALL go only to the reporting member's Profile.

#### Scenario: Member reports a small drift

- **WHEN** a member in another Profile reports 120 ms of drift
- **THEN** the server answers with a nudge, writes one `nudge` row to that member's Profile and
  sends no `transition`

#### Scenario: Reports are rate bounded

- **WHEN** the player measures drift every 250 ms for 12 seconds
- **THEN** it sends at most three drift reports

### Requirement: Member's Up Next defers to the party

On a party member's web player, Up Next and the still-watching autoplay SHALL NOT advance
locally. The host's advance SHALL move every member through the party timeline.

#### Scenario: Title ends on a member's player

- **WHEN** a title ends on a member's player in a party
- **THEN** the player does not start Up Next, and it follows the party when the host advances
