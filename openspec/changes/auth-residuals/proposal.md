# Close three auth residuals: owner transfer, setup throttle, demo enrollment

Ticket: CORE-0728 (`queue:core:main`).

## Why

Three findings from the 2026-08-22 core API audit still hold on main:

- `Store.SetProfileOwner` moves the owner designation but leaves every existing Admin Session
  window in place. Admin authority on a request is derived from that window, so the outgoing
  owner keeps Console authority until the window lapses (up to the Admin Session TTL). Changing
  the admin password, demoting a Device to viewer and switching a session's Profile already clear
  the window on their paths.
- First-run setup failures are throttled on one server-wide lane. A few wrong setup tokens from any
  source push that lane to its maximum lockout, and one more wrong token per window renews it, so
  the operator's correct token is refused with `429`. Pairing-code enrollment dropped the same
  server-wide lane earlier for the same reason.
- An enrollment that presents the opt-in demo grant returns before the remote-enrollment gate. The
  grant is reusable, so with it enabled an off-LAN holder can enroll any number of viewer Devices
  while remote enrollment is turned off.

## What Changes

- `SetProfileOwner` clears the Admin Session window of every session not bound to the new owner,
  in the same transaction as the designation. The new owner's own sessions keep theirs. This also
  covers the first designation on a server that had none, where any adult could hold a window.
- The setup throttle keeps only its per-source lane. A request with no source address shares one
  "unknown source" lane, so the throttle still applies to it.
- Demo-grant enrollment applies the remote-enrollment gate: an off-LAN request is refused with the
  same invalid-code answer as a pairing code when remote enrollment is disabled, and the failure is
  recorded on the demo throttle lane and in the activity log.

## Non-goals

- Treating a session row with no expiry as expired (the audit's fourth, P3 item). The development,
  demo and acceptance seeds store their sessions without an expiry, and client integration suites
  authenticate with those seeded sessions, so that change needs the seeds to carry an expiry first.
  It stays open on CORE-0728.
- Changing the setup token, the backoff curve, or the Admin Session TTL.
- Re-checking the owner designation when a request's Admin authority is derived.
- A schema change. No migration is added.

## Failure modes

- An operator signed in to the Console on two Profiles who transfers ownership loses the Admin
  Session on the outgoing Profile at once and must sign in again as the new owner.
- An app review that enrolls with the demo grant from off the LAN needs the operator to enable
  remote enrollment (it defaults to off); otherwise the reviewer gets an invalid-code answer.
- A flood of wrong setup tokens from one source no longer slows other sources; the 192-bit setup
  token and the per-source lane keep guessing impractical.

## Acceptance evidence

- `internal/store/owner_test.go`: after `SetProfileOwner` moves ownership, the outgoing owner's
  sessions carry no Admin Session window and the new owner's session keeps its own.
- `internal/pairing/auth_residuals_test.go`: repeated wrong setup tokens from one source do not
  throttle a correct token from another source; demo enrollment from off the LAN is refused when
  remote enrollment is disabled and accepted when it is enabled.
- `go test -race ./internal/store/ ./internal/pairing/ -run 'Owner|Setup|Demo'`.

## Rollback

Revert the change. No stored state depends on it.

## Capabilities

### New Capabilities

- `admin-session`: how ownership transfer affects existing Admin Sessions.
- `device-enrollment`: how the demo enrollment grant interacts with the remote-enrollment policy.

### Modified Capabilities

- `first-run-setup`: the setup throttle is per source.
