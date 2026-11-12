# Let an owner get back in after setup

Ticket: CORE-1106 (LOGIN-RECOVERY-1, `queue:core:main`).

## Why

Owners lose the ability to sign in some time after setup. A web session ends hard after 30 days,
and the browser's credential cookies expire with it, so the browser forgets the device too. The
only way back in is a pairing code, and only an existing manager device can mint one. When the
owner's only manager was that browser, nothing can mint a code. `beamfall reset-admin-password`
sets a password but gives no way to use it to sign in. The setup wizard already asks for the admin
password, so the owner holds a credential the server cannot accept for sign-in.

## What Changes

- **Session renewal.** `POST /api/web/session/renew` mints a new 30-day session for the browser's
  device from the device ID and device secret cookies, even when the session has expired or its
  cookie is gone. It refuses a wrong secret, a revoked device, a disabled Profile and an
  integration session, and its failures share the auth backoff curve per source address.
- The device ID and device secret cookies now last 400 days (the browser maximum) instead of the
  session's lifetime, so renewal still works after a session lapses. The session cookie keeps the
  session's expiry. Logout still clears all three.
- The web app renews once and retries when its session check is refused, and only then shows the
  sign-in screen.
- **Admin-password sign-in.** `POST /api/web/admin-signin` takes the admin password and a device
  name and enrolls a new Manager Device on the owner Profile. Plain HTTP is accepted only from the
  local network. Attempts are throttled per source address and server-wide. Every refusal
  (no password set, wrong password, no owner) is one uniform 401.
- The web sign-in page gains a "Sign in with the admin password" form.
- **Host recovery command.** `beamfall pairing-code [--data-dir DIR] [--role manager|viewer]` mints
  a single-use pairing code for the owner Profile from the host, while the server is stopped, in
  the same format and with the same 10-minute expiry as a code minted in the app.

## Risk accepted

Admin-password sign-in makes the admin password a network sign-in credential, not only a
re-authentication step. It is limited to HTTPS or the local network, throttled per source and
server-wide, and audited. A server-wide lane lets a remote guesser delay the owner's own sign-in;
the owner still has the host command and the pairing-code path.

## Impact

- `internal/store`: a session rotation method (insert the new session and delete the old one in
  one transaction) and a lookup of a device's latest session. No migration.
- `internal/pairing`: `RenewSession`, `AdminSignIn` and a reusable pairing-code generator.
- `internal/server`: the two handlers, long-lived device cookies, route registries.
- `cmd/beamfall`: the `pairing-code` command.
- `internal/web/app`: renew-and-retry in the session spine; the admin-password form.
- `docs/security/runbooks/lockout-and-recovery.md`, `README.md`.
