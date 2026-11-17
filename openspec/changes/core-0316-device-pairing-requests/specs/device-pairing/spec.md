## ADDED Requirements

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
