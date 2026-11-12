# Let the owner choose Home or Strict sign-in security at setup

Ticket: CORE-1110 (`queue:core:main`). Amends ADR-0007 (owner decision).

## Why

CORE-1106 lets an unenrolled browser sign in with the admin password. That suits most homes, where
the owner wants an easy way back in. Some owners want no password sign-in at all, so that only an
enrolled device or the host can add a device. The owner should pick, once, at setup, and be able to
change it later.

## What Changes

- First-run setup accepts an optional `securityMode`: `home` (the default) or `strict`. It is
  stored in the same transaction as the owner Profile and first device, in the existing
  `server_config` key-value table. No migration.
- In strict mode, `POST /api/web/admin-signin` refuses every request with the same uniform 401 as
  any other refusal. It still verifies the password and records the failure in the same backoff
  lanes, so the mode cannot be told apart by timing or by counters.
- Device-secret session renewal, pairing codes from a Manager and `beamfall pairing-code` keep
  working in both modes.
- `GET /api/setup` reports the mode, so the sign-in page can hide the password form in strict mode.
  It only reveals which sign-in path exists.
- The setup wizard gets a security step: Home (recommended) or Strict.
- `POST /api/web/signin-security` changes the mode. It needs a Manager session and the admin
  password in the same request. A wrong password counts against the caller's admin re-auth backoff
  lane. Each change is audited with the old and new mode, never the password.
- The Console gets a setting for it.

## Impact

- `internal/store`: read and write the mode; `BootstrapOwner` stores it.
- `internal/pairing`: setup takes the mode; admin sign-in refuses in strict mode.
- `internal/auth`: the password-checked mode change, throttle and audit.
- `internal/server`: the probe field, the new route and its registrations.
- `internal/web/app`: the wizard step, the sign-in page and the Console setting.
- `docs/security/runbooks/lockout-and-recovery.md`.
