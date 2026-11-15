# Device list: show a browser that signed out as signed out

Tickets: CORE-1620 (`queue:core:main`)

## Why

Signing a browser out deletes its session row but leaves the device unrevoked. Console > Profiles &
access > Devices only knew "revoked" or "active", so a signed-out browser showed as Active even
though it cannot renew a session and must enroll again (onboarding audit 2026-11-14, finding 9).

## What changes

- `DeviceInfo` (the `devices` and `setDeviceRole` results) gains `signedIn: Boolean`. It is true
  while the device holds an enrolled session row, including an expired row the device can still
  renew.
- The Console device list shows a "Signed out" chip for an unrevoked device with
  `signedIn: false`, in place of "Active". Revoked still wins.
- This is a wire-contract change, so it ships in the client-core-wire 2.10.0 lockstep: core
  publishes the artifact, the consumers re-vendor, and then core and the workspace registry pin it.

## Non-goals

- No change to sign-out, revocation or session renewal.
