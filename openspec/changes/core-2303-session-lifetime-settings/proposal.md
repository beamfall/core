# Let the owner set how long sessions last

Ticket: CORE-2303 (`queue:core:main`).

## Why

The owner reports that sessions expire too quickly and wants the lifetime to be editable, with
the option to switch expiry off. Both lifetimes are constants today:

- The Admin Session (the Console unlock) lasts 15 minutes from the unlock and is not extended by
  activity (`adminSessionTTL`, ADR-0007 §2).
- A sign-in session lasts 30 days from enrollment or renewal. A browser renews it from its
  device cookie; a client that does not renew has to be paired again.

## What Changes

- Two Console config values, read and written through `consoleConfig` and `setConsoleConfig`:
  `sessionLifetimeDays` (default 30) and `adminSessionMinutes` (default 15). `0` means the
  session never expires.
- Enrollment, renewal and the Admin Session unlock use the configured lifetime.
- A change re-bases live sessions: every unexpired sign-in session, or every open Admin Session
  window, restarts from the time of the change with the new lifetime.
- The Console Config section gets a "Sessions" group with both settings, a "Never expire"
  choice in each and one line on the trade-off.
- Ending a session is unchanged: lock, sign-out, device revocation, profile switch, ownership
  transfer and an admin password change still end it, whatever the lifetime.

A session that never expires is reported with the expiry `9999-12-31T23:59:59Z`, so the
`sessionExpiresAt` and `adminSession.expiresAt` fields keep their type and clients need no change.

## Impact

`internal/store`, `internal/auth`, `internal/pairing`, `internal/graph`, `internal/web/app`. Two
optional `setConsoleConfig` variables and two `ConsoleConfig` fields are added to the graph
surface. No migration. ADR-0007 §2 gave 15 minutes as the Admin Session window; that value stays
the default and the owner may now change it (owner decision 2026-11-17).
