## ADDED Requirements

### Requirement: The first admin password can be chosen from the Console

When no admin password exists, an adult owner on a Manager device SHALL be able to choose the
first admin password through `verifyAdminSession` with `createFirst` set. The server SHALL store it
only if none exists and SHALL never replace an existing one. The attempt SHALL count on the admin
re-auth lane, be audited without the password, and fail with the generic admin-password error.

#### Scenario: No password yet

- **WHEN** the owner on a Manager device sends `verifyAdminSession` with a password and
  `createFirst` set, and no admin password exists
- **THEN** the password is stored, the Admin Session opens, and an `admin.password.set` entry is
  recorded

#### Scenario: Password already exists

- **WHEN** the same request arrives after a password exists
- **THEN** the stored password is unchanged and the request is checked as an ordinary unlock

#### Scenario: Not a Manager or not the owner

- **WHEN** a non-Manager device or a non-owner Profile sends the request with no password set
- **THEN** no password is stored and the generic admin-password error is returned

### Requirement: The Console offers the set-password step

The Console gate SHALL show a choose-and-confirm admin password form instead of the unlock prompt
when the setup state reports no admin password and the session is an adult Profile on a Manager
device. Otherwise it SHALL show the unlock prompt.

#### Scenario: No password set

- **WHEN** the owner opens the Console and the setup state reports no admin password
- **THEN** the gate asks for a new password and a confirmation, not the current password
