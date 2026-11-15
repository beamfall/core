## ADDED Requirements

### Requirement: A device can request pairing without a typed code

The server SHALL let a device with no credential request pairing. The response SHALL carry a short
display code and a separate request secret. The server SHALL NOT enroll the device until an
administrator approves the code.

#### Scenario: Request and wait

- **WHEN** a device posts a valid device name to `/api/pairing-requests`
- **THEN** the server answers `201` with `code`, `requestSecret`, `expiresAt` and `pollIntervalSeconds`
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
Request, poll and regenerate SHALL share the enrollment throttle keyed by remote address. The device
name SHALL be bounded and SHALL NOT contain control or invisible formatting characters.

#### Scenario: Expired request never enrolls

- **WHEN** a request expires before approval, or after approval but before collection
- **THEN** approval and collection are refused

#### Scenario: Regenerate replaces the code and secret

- **WHEN** a device regenerates a pending request
- **THEN** it gets a new code and secret and the old ones stop working

#### Scenario: Guessing is throttled

- **WHEN** one address keeps polling with unknown secrets
- **THEN** after the free attempts the server answers `429`

### Requirement: Existing pairing switches apply to pairing requests

The remote-enrollment setting and the Viewer pairing-code switch SHALL apply at request, approval
and collection, with the existing invalid-code answer.

#### Scenario: Viewer pairing codes disabled

- **WHEN** Viewer pairing codes are disabled
- **THEN** a Viewer request, a Viewer approval and the collection of a Viewer approval are refused
