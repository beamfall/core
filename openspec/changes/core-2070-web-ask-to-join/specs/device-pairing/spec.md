## ADDED Requirements

### Requirement: A browser poll binds the browser like web enrollment

When a pairing-request poll carries the browser CSRF header, the server SHALL apply the
`/api/web/enroll` transport rule before polling, and on approval SHALL set the same credential
cookies `/api/web/enroll` sets and SHALL NOT return the device secret or session ID in the body.
A poll without the header SHALL be unchanged.

#### Scenario: Approved browser poll sets cookies

- **WHEN** an administrator has approved a request and the browser polls with its secret and the CSRF header
- **THEN** the poll answers `approved` and sets the session, device and device-secret cookies as HttpOnly and SameSite=Strict
- **AND** the body carries no device secret and the cookies authenticate the browser

#### Scenario: Off-LAN plain HTTP browser poll refused

- **WHEN** a browser polls with the CSRF header over plain HTTP from outside the local network
- **THEN** the server answers `403` and the request stays pending

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

#### Scenario: Leaving the page stops polling

- **WHEN** the enroll page unmounts while a request is pending
- **THEN** no further poll is sent

#### Scenario: Refusals read differently

- **WHEN** the request is denied, expires, is throttled or is refused
- **THEN** the page reports each with its own plain message and the typed-code form stays usable
