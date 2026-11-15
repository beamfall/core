# Pairing requests name the device to the administrator

Ticket: CORE-2063 (`queue:core:main`)

## Why

With device-initiated pairing (CORE-0316) the administrator approves a request from a list. A code
and a device name alone are not enough to approve safely: the administrator needs to see what kind
of device is asking, what it wants to be, and whether it is on the local network.

## What changes

- `POST /api/pairing-requests` accepts two optional fields: `platform` (a free label such as
  `tvos` or `android`) and `requestedRole` (`viewer`, the default, or `manager`).
- Each entry of `GET /api/pairing-requests` adds `platform`, `requestedRole` and `lan`. `lan` is
  derived by the server from the connection, with the same rule `/api/enroll` uses for the server
  key pin; a client cannot supply it.
- Approval's `deviceRole` defaults to the requested role instead of always `viewer`.
- `docs/api/lan-pairing.md` documents the fields.

No route, response field or wire-contract change beyond those fields; the management manifest and
wire goldens do not describe request bodies, so nothing is regenerated.

## Non-goals

- No administrator role through pairing requests, ever.
- No verification of the platform label; it is display text only.

## Failure modes

- `platform` is untrusted: trimmed, at most 32 characters, no control or invisible formatting
  characters, otherwise `400`. It is optional.
- `requestedRole` other than `viewer` or `manager` (case-sensitive), `admin` included, is refused
  with the invalid-code answer and counts as a throttle failure.
- A `manager` request is only a request. Nothing is enrolled without an administrator's approval,
  and the administrator may grant `viewer` instead.
- With Viewer pairing codes disabled, a `manager` request is accepted (the switch governs Viewer
  pairing only) and an approval that grants `viewer` is refused.

## Acceptance evidence

- `internal/pairing` tests: identity in the pending list, `lan` from the connection, role refusal,
  manager approval default and downgrade, Viewer switch with a manager request, platform validation.
- `internal/server` test: the list fields over HTTP, a client-supplied `lan` ignored, `admin` role
  refused with `401`, a control character in `platform` refused with `400`.
- Conformance `WireContract|Mgmt|Route`, untagged and App Store tagged, unchanged.

## Rollback

Revert the commit. Pending requests are in memory only. Clients that send the new fields to a
server without them are unaffected: unknown fields are ignored and every request is `viewer`.
