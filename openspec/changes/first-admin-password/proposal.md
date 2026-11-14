# Let the owner choose the first admin password from the Console

Ticket: CORE-1132 (`queue:core:main`). Restores ADR-0007 §2 (the admin password is set at first run)
for the case CORE-1105 opened: setup finished from a LAN browser with no token and no password.

## Why

When the owner finishes first-run setup without a password, no admin password exists. The Console
asks for the admin password and has nothing to check it against, so it stays locked. The only way
out is `beamfall reset-admin-password` with the server stopped.

## What Changes

- The Console gate offers a "choose an admin password" step instead of the unlock prompt when the
  setup state reports no admin password and the caller is an adult Profile on a Manager device.
- The step submits through the existing `verifyAdminSession` mutation with an extra
  `createFirst: true` variable. No new route, field or mutation, so the wire contract is unchanged.
- The server sets the password only if none exists (insert-if-absent, one transaction), then opens
  the Admin Session window. It needs a Manager device, an adult Profile and the owner (or no owner
  designated), as the existing unlock does.
- The attempt counts on the same admin re-auth lane, each outcome is audited, and every refusal
  returns the same generic error as a wrong password.
- Once a password exists the request is an ordinary unlock; `createFirst` is ignored and can never
  replace a password.

## Impact

- `internal/store`: `SetAdminPasswordIfAbsent`.
- `internal/auth`: `CreateFirstAdminPassword`, used by `BeginAdminSession`'s caller in `internal/graph`.
- `internal/graph`: `verifyAdminSession` reads `createFirst`.
- `internal/web/app`: the Console gate's set-password form and strings.
