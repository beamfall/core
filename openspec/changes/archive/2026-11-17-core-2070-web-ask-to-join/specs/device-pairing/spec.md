## ADDED Requirements

### Requirement: A browser poll binds the browser like web enrollment

A pairing request made with the CSRF, `Origin` or `Sec-Fetch-Site` header SHALL be a browser
request for its whole life. For a browser request the server SHALL apply the
`/api/web/enroll` transport rule before the request is touched, and on approval SHALL set the
credential cookies `/api/web/enroll` sets and SHALL NOT return the device secret or session ID in
the body. Other requests SHALL be unchanged.

#### Scenario: Approved browser poll sets cookies

- **WHEN** an administrator has approved a request and the browser polls with its secret and the CSRF header
- **THEN** the poll answers `approved` and sets the session, device and device-secret cookies as HttpOnly and SameSite=Strict
- **AND** the body carries no device secret and the cookies authenticate the browser

#### Scenario: Off-LAN plain HTTP browser poll refused

- **WHEN** a browser polls with the CSRF header over plain HTTP from outside the local network
- **THEN** the server answers `403` and the request stays pending

#### Scenario: The header cannot be shed at poll

- **WHEN** a request made with the CSRF header is approved and then polled without the header
- **THEN** the poll sets the credential cookies and the body carries no device secret or session ID

#### Scenario: The header cannot be shed at request

- **WHEN** a request is made without the CSRF header but with an `Origin` or `Sec-Fetch-Site` header, and is approved and polled
- **THEN** the poll sets the credential cookies and the body carries no device secret or session ID

#### Scenario: Off-LAN plain HTTP browser request and regenerate refused

- **WHEN** a browser requests or regenerates with the CSRF header over plain HTTP from outside the local network
- **THEN** the server answers `403` and creates or replaces nothing

#### Scenario: Native poll unchanged

- **WHEN** a device polls an approved request without the CSRF header
- **THEN** the body carries the enrollment credential and no cookie is set

### Requirement: The web enroll page can ask to join

The web enroll page SHALL offer an Ask to join action that displays the request code, polls at the
server's interval and on approval binds the browser as typed enrollment does. The request secret
SHALL stay in memory only. Starting typed enrollment or asking SHALL cancel the other.

#### Scenario: Approved ask signs the browser in

- **WHEN** the user asks to join and an administrator approves the displayed code
- **THEN** the page binds the browser and reloads to the app root

#### Scenario: Older server

- **WHEN** the request route answers `404`
- **THEN** the Ask to join action is hidden and the typed-code form stays

#### Scenario: Refusals read differently

- **WHEN** the request is denied, expires, is throttled or is refused
- **THEN** the page reports each with its own plain message and the typed-code form stays usable

### Requirement: The web enroll page withdraws and times its request by the server

The web enroll page SHALL withdraw on the server a request it stops wanting. It SHALL time the code
by the lifetime the server gave, not by the browser's clock, and SHALL NOT abandon a poll that may
be collecting an approval in order to fetch a new code.

#### Scenario: Leaving the page stops polling

- **WHEN** the enroll page unmounts, reloads or closes while a request is pending
- **THEN** no further poll is sent and the request is withdrawn

#### Scenario: Cancel withdraws the request

- **WHEN** the user presses Cancel or submits a typed code while a request is pending
- **THEN** the page withdraws the request so it leaves the administrator's list

#### Scenario: A wrong browser clock

- **WHEN** the browser's clock is ahead of the server's by more than the code's lifetime
- **THEN** the page keeps polling until the server's lifetime runs out

#### Scenario: New code during a collecting poll

- **WHEN** the user asks for a new code while a poll is on the wire and that poll answers `approved`
- **THEN** the page binds the browser and requests no new code
