## ADDED Requirements

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
invalid-code answer. A request SHALL NOT enroll the device until an administrator approves it, and
the approval SHALL default to the requested role while allowing the administrator to grant another
non-administrator role.

#### Scenario: Administrator role refused

- **WHEN** a device requests the `admin` role
- **THEN** the server refuses with the invalid-code answer and counts a throttle failure

#### Scenario: Manager request waits for approval

- **WHEN** a device requests `manager` and polls before approval
- **THEN** the poll answers `pending` and nothing is enrolled
- **AND** an approval without `deviceRole` enrolls the device as `manager`

#### Scenario: Viewer switch with a manager request

- **WHEN** Viewer pairing codes are disabled and a device requests `manager`
- **THEN** the request is accepted and waits for approval
- **AND** an approval that grants `viewer` is refused with the invalid-code answer
