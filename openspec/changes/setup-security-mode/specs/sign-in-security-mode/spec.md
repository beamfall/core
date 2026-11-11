## ADDED Requirements

### Requirement: Setup stores the sign-in security mode

First-run setup SHALL accept an optional `securityMode` of `home` or `strict`, defaulting to `home`,
and SHALL store it in the same transaction as the owner Profile and first device. Any other value
SHALL be refused with 400 and store nothing.

#### Scenario: Setup in strict mode

- **WHEN** setup completes with `securityMode` set to `strict`
- **THEN** the stored mode is `strict`

#### Scenario: Setup without a mode

- **WHEN** setup completes without `securityMode`
- **THEN** the stored mode is `home`

### Requirement: Strict mode refuses admin-password sign-in uniformly

In strict mode, admin-password sign-in SHALL refuse every request with the same 401 as any other
refusal. It SHALL still verify the password and record the failure in the same per-source and
server-wide backoff lanes, so the mode cannot be learned from timing or counters. The mode SHALL be
read in the transaction that would enroll the device.

#### Scenario: Correct password in strict mode

- **WHEN** an unenrolled browser presents the correct admin password in strict mode
- **THEN** sign-in is refused with 401, no device is created, and one failure is recorded

#### Scenario: Switch to strict during sign-in

- **WHEN** the mode changes to strict while a home-mode sign-in waits for its password check
- **THEN** sign-in is refused with the same 401 and no device is created

#### Scenario: Correct password in home mode

- **WHEN** an unenrolled browser presents the correct admin password in home mode
- **THEN** a Manager device is enrolled on the owner Profile

### Requirement: Other recovery paths work in both modes

Session renewal with the device secret, pairing codes minted by a Manager and `beamfall
pairing-code` SHALL work the same in home and strict mode.

#### Scenario: Renewal in strict mode

- **WHEN** an enrolled browser renews its session in strict mode
- **THEN** a new session is issued

### Requirement: The setup probe reports the mode

`GET /api/setup` SHALL report the stored mode as `securityMode` without authentication. The web
sign-in page SHALL show the admin-password form only once the probe reports home. When the probe
fails the mode SHALL stay unknown: the Console switch SHALL show an error with a retry and SHALL NOT
save.

#### Scenario: Strict mode hides the password form

- **WHEN** the probe reports `strict`
- **THEN** the sign-in page shows only the pairing-code form

#### Scenario: Probe fails

- **WHEN** the probe request fails
- **THEN** the sign-in page shows only the pairing-code form and the Console switch offers a retry
  and no save

### Requirement: Changing the mode needs a Manager and the admin password

`POST /api/web/signin-security` SHALL change the mode only for the owner's Manager session with the
correct admin password in the same request, rechecking owner and session in the write's
transaction. A wrong or missing password SHALL be refused with 401 and count against the admin
re-auth backoff lane of the caller's Profile, which a renewed session shares; a throttled caller
SHALL get 429.

#### Scenario: Ownership moves during the password check

- **WHEN** ownership is transferred after the caller's checks pass and before the mode is written
- **THEN** the change is refused with 401 and the mode is unchanged

#### Scenario: Wrong password

- **WHEN** a Manager sends a new mode with a wrong password
- **THEN** the change is refused with 401 and the mode is unchanged

#### Scenario: Viewer device

- **WHEN** a Viewer device sends a new mode with the correct password
- **THEN** the change is refused with 403

### Requirement: Mode changes are audited

Each accepted mode change SHALL append one activity entry naming the old and new mode and the
calling session, device and Profile. The entry SHALL never contain the password.

#### Scenario: Audit entry

- **WHEN** a Manager switches from home to strict
- **THEN** one activity entry records `home` and `strict` and the caller
