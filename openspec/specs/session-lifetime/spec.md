# session-lifetime Specification

## Purpose
How long a sign-in session and a Console unlock (Admin Session) last. The owner sets both in the
Console config and may switch either expiry off; lock, sign-out and revocation still end a session.

## Requirements

### Requirement: The owner sets the sign-in session lifetime

The Console config SHALL carry `sessionLifetimeDays`, defaulting to 30. A value of 0 SHALL mean a
sign-in session never expires; any other value SHALL be between 1 and 3650. A sign-in session
created by enrollment or renewal SHALL expire that many days after it is created.

#### Scenario: No value has been set

- **WHEN** a device enrolls on a server whose owner has not changed the setting
- **THEN** its session expires 30 days after enrollment

#### Scenario: The owner chooses 90 days

- **WHEN** the owner sets `sessionLifetimeDays` to 90 and a device then enrolls
- **THEN** its session expires 90 days after enrollment

#### Scenario: The owner chooses never

- **WHEN** the owner sets `sessionLifetimeDays` to 0 and a device then enrolls
- **THEN** its session is still accepted years later, and its reported expiry is
  `2200-01-01T00:00:00Z`

### Requirement: The owner sets the Admin Session lifetime

The Console config SHALL carry `adminSessionMinutes`, defaulting to 15. A value of 0 SHALL mean
the Admin Session never expires; any other value SHALL be between 5 and 43200. An Admin Session
opened with the admin password SHALL last that many minutes from the unlock.

#### Scenario: No value has been set

- **WHEN** the owner unlocks the Console on a server with the default setting
- **THEN** the Admin Session ends 15 minutes later

#### Scenario: The owner chooses never

- **WHEN** `adminSessionMinutes` is 0 and the owner unlocks the Console
- **THEN** the Admin Session is still active a year later

### Requirement: A lifetime change applies to live sessions

When `sessionLifetimeDays` changes, every unexpired sign-in session SHALL expire the new lifetime
after the change. When `adminSessionMinutes` changes, every open Admin Session window SHALL end
the new lifetime after the change. An expired session or a closed window SHALL stay so, and an
integration session SHALL keep its own expiry.

#### Scenario: The owner switches Admin Session expiry off while unlocked

- **WHEN** the owner sets `adminSessionMinutes` to 0 from an unlocked Console
- **THEN** that Admin Session no longer ends after 15 minutes

#### Scenario: The owner shortens the sign-in lifetime

- **WHEN** sessions never expire and the owner sets `sessionLifetimeDays` to 7
- **THEN** every live sign-in session expires 7 days after the change

### Requirement: A session without expiry can still be ended

Locking the Console, signing out, revoking the device, switching profile, transferring ownership
and changing the admin password SHALL end a session or Admin Session exactly as they do for one
that expires.

#### Scenario: The owner locks a never-expiring Admin Session

- **WHEN** `adminSessionMinutes` is 0 and the owner locks the Console
- **THEN** Console operations are refused until the admin password is entered again

### Requirement: The lifetimes are changed only through the audited config write

Both values SHALL be written only by `setConsoleConfig`, which requires an Admin Session and
records a config update in the activity log.

#### Scenario: A caller without an Admin Session

- **WHEN** a caller without an open Admin Session sends `setConsoleConfig` with
  `adminSessionMinutes`
- **THEN** the write is refused and the stored value is unchanged
