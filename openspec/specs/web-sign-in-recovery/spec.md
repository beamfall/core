# web-sign-in-recovery Specification

## Purpose
An owner who has lost every signed-in manager device can get back into the web UI with the admin password, under the active sign-in security mode, instead of being locked out once sessions and device cookies expire.

## Requirements

### Requirement: A browser can renew its session with its device credential

The server SHALL mint a new 30-day session for a browser that presents its device ID and device
secret cookies and the CSRF header, even when its session cookie is expired or missing. It SHALL
replace the device's one session row in the same write, keeping its Profile. A row minted in the
last minute SHALL be returned instead only while it has no Reveal or admin window, so overlapping
renewals converge. Plain HTTP SHALL be accepted only from the local network.

#### Scenario: Expired session renewed

- **WHEN** a browser whose session has expired presents a valid device ID and device secret
- **THEN** a new session for the same device and Profile is created, the old one is deleted, and
  the session cookie is set to the new session

#### Scenario: Session cookie missing

- **WHEN** a browser presents a valid device credential but no session cookie
- **THEN** the device's session row is replaced, not added to, so the device still has one session

#### Scenario: Two tabs renew at once

- **WHEN** two tabs of one browser renew at the same moment
- **THEN** both get the same live session and the device still has one session

#### Scenario: Recent session is unlocked

- **WHEN** a browser renews while its session minted a moment ago has Reveal or an admin window
- **THEN** that session is deleted and a new locked session is returned

#### Scenario: Terminated session

- **WHEN** a session was ended from Live Sessions and its browser then renews
- **THEN** renewal is refused with 401 and the browser must sign in again

### Requirement: Session renewal refuses an invalid device credential

Renewal SHALL refuse with 401 and create nothing when the device secret is wrong, the device is
revoked, either device cookie is missing, the Profile is disabled, or the device has no session
row. Failures SHALL count against the auth backoff curve keyed per source address, and a throttled
source SHALL be refused with 429.

#### Scenario: Wrong device secret

- **WHEN** a browser presents its device ID with a wrong device secret
- **THEN** renewal is refused with 401 and no session is created

#### Scenario: Revoked device

- **WHEN** a revoked device presents its correct device secret
- **THEN** renewal is refused with 401 and no session is created

#### Scenario: Missing device cookies

- **WHEN** a request carries no device ID or no device secret cookie
- **THEN** renewal is refused with 401

#### Scenario: Plain HTTP from the internet

- **WHEN** a renewal request arrives over plain HTTP from a public address
- **THEN** it is refused with 403 before any credential is checked

### Requirement: Device cookies outlive the session

The server SHALL set the device ID and device secret cookies with a 400-day expiry wherever it sets
browser credentials (enroll, setup, promote, renewal and admin-password sign-in). The session
cookie SHALL keep the session's expiry. Logout SHALL clear all three cookies.

#### Scenario: Enrollment cookies

- **WHEN** a browser enrolls with a pairing code
- **THEN** the device ID and device secret cookies expire 400 days later and the session cookie
  expires with the session

### Requirement: The web app renews before signing out

When the web app's session check is refused as unauthorized, the app SHALL request a session
renewal once and retry the check. Only when the renewal or the retried check fails SHALL the app
show the sign-in screen.

#### Scenario: Lapsed session

- **WHEN** the web app loads with an expired session and valid device cookies
- **THEN** the app renews the session and opens normally, without showing the sign-in screen

#### Scenario: Renewal refused

- **WHEN** the session check and the renewal are both refused
- **THEN** the app shows the sign-in screen

### Requirement: The owner can sign in with the admin password

The server SHALL accept the admin password and a device name, with the CSRF header, and enroll a
new Manager Device with a 30-day session on the owner Profile, setting the browser's credential
cookies and recording the enrollment in the activity log. Plain HTTP SHALL be accepted only from
the local network and only when the Host is one a public site cannot rebind (an IP literal,
localhost, a .localhost or .local name); other requests need HTTPS.

#### Scenario: Local-network sign-in

- **WHEN** a local-network browser submits the correct admin password and a device name
- **THEN** a new Manager Device is enrolled on the owner Profile and its credential cookies are set

#### Scenario: Plain HTTP from the internet

- **WHEN** an admin-password sign-in arrives over plain HTTP from a public address
- **THEN** it is refused with 403 and the password is not checked

#### Scenario: Rebindable host name on the local network

- **WHEN** a local-network browser sends an admin-password sign-in over plain HTTP addressed to
  `attacker.example`
- **THEN** it is refused with 403 and the password is not checked

### Requirement: Admin-password sign-in refuses uniformly and is throttled

Admin-password sign-in SHALL refuse with one 401 "sign-in failed" response when no admin password
is set, the password is wrong, or there is no owner Profile, so a caller cannot tell these apart.
Attempts SHALL be recorded and throttled with the admin re-auth backoff curve on two keys, the
source address and one server-wide key; a throttled request SHALL be refused with 429.

#### Scenario: Wrong password

- **WHEN** a request submits a wrong admin password
- **THEN** it is refused with 401 "sign-in failed" and no device is created

#### Scenario: No admin password set

- **WHEN** no admin password has been set and a request submits any password
- **THEN** it is refused with the same 401 "sign-in failed"

#### Scenario: Repeated failures

- **WHEN** one source keeps submitting wrong passwords past the free attempts
- **THEN** further attempts are refused with 429 until the backoff delay passes, even with the
  right password

### Requirement: Admin-password checks are bounded and counted first

At most two admin-password verifications SHALL run at once in the server process, across
admin-password sign-in, Admin Session re-auth and the sign-in security switch. Each attempt SHALL
be counted against its backoff lanes before the password is verified. A request that cannot start
a verification within two seconds SHALL get the same refusal as a wrong password and SHALL NOT be
counted.

#### Scenario: Parallel guesses

- **WHEN** one source sends many wrong admin passwords at the same moment
- **THEN** exactly the free attempts are verified and refused with 401, and the rest are refused
  with 429

### Requirement: The host can mint a pairing code

`beamfall pairing-code` SHALL mint a single-use pairing code for the owner Profile, with the role
from `--role` (manager by default, or viewer), in the same format and with the same 10-minute expiry
as a code minted in the app. It SHALL take `--data-dir` or `BEAMFALL_DATA_DIR`, refuse to run while
the server holds the data-directory lock, and print the code and where to enter it.

#### Scenario: Owner locked out of every device

- **WHEN** the operator stops the server and runs `beamfall pairing-code` on the host
- **THEN** it prints a manager pairing code that enrolls a browser on the owner Profile

#### Scenario: Server running

- **WHEN** the server holds the data-directory lock
- **THEN** the command refuses and mints nothing

#### Scenario: No owner

- **WHEN** the data directory has no owner Profile
- **THEN** the command refuses and mints nothing
