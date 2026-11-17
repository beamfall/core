# device-pairing Specification

## Purpose
How a device asks to join a Home on the local network, how a manager reviews and answers the request, and what the device receives.

## Requirements

### Requirement: A device can request pairing without a typed code

The server SHALL let a device with no credential request pairing. The response SHALL carry a short
display code and a separate request secret. The server SHALL NOT enroll the device until an
administrator approves the code.

#### Scenario: Request and wait

- **WHEN** a device posts a valid device name to `/api/pairing-requests`
- **THEN** the server answers `201` with `code`, `requestSecret`, `expiresAt`, `expiresInSeconds` and `pollIntervalSeconds`
- **AND** polling with the secret answers `pending` and no device exists yet

#### Scenario: Approval enrolls the polling device once

- **WHEN** an administrator approves the code and the device polls with its secret
- **THEN** the poll answers `approved` with an enrollment in the `/api/enroll` response shape
- **AND** a second poll with the same secret is refused

#### Scenario: The display code is not a credential

- **WHEN** anyone polls with the display code, or redeems it through `/api/enroll`
- **THEN** the server refuses with the invalid-code answer

### Requirement: Only an administrator decides pairing requests

Listing, approving and denying pairing requests SHALL require an active Admin Session; other
sessions SHALL get `403`.

#### Scenario: Non-administrator refused

- **WHEN** a session without an active Admin Session lists, approves or denies
- **THEN** the server answers `403`

#### Scenario: Deny

- **WHEN** an administrator denies a pending code
- **THEN** the device's next poll answers `denied` and enrolls nothing
- **AND** the code can no longer be approved

### Requirement: Pairing requests are bounded and short-lived

Pending requests SHALL expire after 10 minutes and SHALL be capped in total and per remote address.
Request, poll, regenerate and cancel SHALL share one pairing-request throttle keyed by remote
address, separate from the throttle `/api/enroll` uses. Reaching a cap SHALL NOT count as a failed
attempt. Request and poll answers SHALL carry the lifetime left as a duration on the server's
clock. The device name SHALL be bounded and SHALL NOT contain control or invisible formatting
characters.

#### Scenario: Expired request never enrolls

- **WHEN** a request expires before approval, or after approval but before collection
- **THEN** approval and collection are refused

#### Scenario: Regenerate replaces the code and secret

- **WHEN** a device regenerates a pending request
- **THEN** it gets a new code and secret and the old ones stop working

#### Scenario: Guessing is throttled

- **WHEN** one address keeps polling with unknown secrets
- **THEN** after the free attempts the server answers `429`

#### Scenario: Stale polls do not lock out code enrollment

- **WHEN** one address has been throttled for polling with unknown secrets
- **THEN** the same address can still redeem a valid pairing code through `/api/enroll`

#### Scenario: A full source is not a failed attempt

- **WHEN** an address that already holds the per-address maximum of pending requests asks again
- **THEN** the server answers `429` and records no failed attempt

### Requirement: A device can withdraw its pairing request

The server SHALL let a device withdraw a pending request with its request secret. The answer SHALL
be the same whether or not the secret named a live request. A request whose approval is being
collected SHALL NOT be withdrawn.

#### Scenario: Cancel removes the request

- **WHEN** a device posts its request secret to `/api/pairing-requests/cancel`
- **THEN** the server answers `204`, the request leaves the pending list and its code can no longer be approved
- **AND** a later poll with that secret is refused

#### Scenario: Cancel is not an oracle

- **WHEN** anyone posts an unknown secret to `/api/pairing-requests/cancel`
- **THEN** the server answers `204` and records no failed attempt

### Requirement: Existing pairing switches apply to pairing requests

The remote-enrollment setting and the Viewer pairing-code switch SHALL apply at request, approval
and collection, with the existing invalid-code answer. Regenerate SHALL apply the remote-enrollment
setting as the request does, and a regenerated request SHALL count as on the local network only
when both the original request and the regenerate were.

#### Scenario: Regenerate from off the local network

- **WHEN** remote enrollment is off and a regenerate arrives from outside the local network
- **THEN** the server refuses with the invalid-code answer and the old code and secret keep working

#### Scenario: Viewer pairing codes disabled

- **WHEN** Viewer pairing codes are disabled
- **THEN** a Viewer request, a Viewer approval and the collection of a Viewer approval are refused

### Requirement: A pairing request names the device to the administrator

A pairing request SHALL accept an optional platform label and an optional requested role. Each
pending list entry SHALL carry the device name, platform, requested role, short code and whether
the request came from the local network. The local-network flag SHALL be derived by the server and
SHALL NOT be taken from the client.

#### Scenario: Identity in the pending list

- **WHEN** a device on the local network requests pairing with a name, a platform and the `manager` role
- **THEN** the administrator's pending list shows that name, platform, `manager` and `lan: true` with the code

#### Scenario: Client cannot claim the local network

- **WHEN** a request body carries `lan: false` or `lan: true`
- **THEN** the pending entry's `lan` reflects only the connection the request arrived on

#### Scenario: Untrusted platform

- **WHEN** the platform has a control or invisible formatting character or is longer than 32 characters
- **THEN** the server answers `400` and records no request

### Requirement: A requested role is granted only by approval

A requested role SHALL be `viewer` or `manager`. Any other value SHALL be refused with the
invalid-code answer. A request SHALL NOT enroll the device until an administrator approves it. An
approval SHALL grant `viewer` unless it names another non-administrator role: the requested role
SHALL NOT be granted by an approval that names none.

#### Scenario: Administrator role refused

- **WHEN** a device requests the `admin` role
- **THEN** the server refuses with the invalid-code answer and counts a throttle failure

#### Scenario: Manager request waits for approval

- **WHEN** a device requests `manager` and polls before approval
- **THEN** the poll answers `pending` and nothing is enrolled
- **AND** an approval without `deviceRole` enrolls the device as `viewer`

#### Scenario: Manager is granted only when named

- **WHEN** a device requests `manager` and the administrator approves with `deviceRole: "manager"`
- **THEN** the device is enrolled as `manager`

#### Scenario: Viewer switch with a manager request

- **WHEN** Viewer pairing codes are disabled and a device requests `manager`
- **THEN** the request is accepted and waits for approval
- **AND** an approval that grants `viewer`, by name or by default, is refused with the invalid-code answer

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
