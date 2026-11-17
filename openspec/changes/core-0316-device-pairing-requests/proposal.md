# Device-initiated pairing requests

Ticket: CORE-0316 (`queue:core:main`)

## Why

Today a device pairs only by redeeming a code an administrator minted and typed into it. For
tap to connect, the device should start pairing itself: it asks the server for a short code,
shows it, and waits, and an administrator approves it with one click in the Console. No code is
typed anywhere.

## What changes

- Seven client-neutral routes under `/api/pairing-requests`: request, poll, regenerate and cancel
  for the device (no credential, like `/api/enroll`), and list, approve and deny for an
  administrator.
- Cancel withdraws a request with its request secret, so a device that stops wanting it does not
  leave it pending for 10 minutes. It answers `204` whether or not the secret named a live request.
- A request returns a short display `code` and a separate `requestSecret`. Only the secret polls,
  so the credential reaches only the device that made the request, never someone who saw the code.
- Approval needs an active Admin Session and an enabled profile. The next poll enrolls the device
  with the existing `EnrollDevice` path and returns the `/api/enroll` response shape, once.
- Pending requests are in memory, expire after 10 minutes, and are capped at 64 in total and 3 per
  remote address. Request, poll, regenerate and cancel share one pairing-request throttle keyed by
  remote address. It is separate from the `/api/enroll` throttle, so a device polling a secret the
  server no longer knows cannot lock its address out of code enrollment. Hitting a cap answers
  `429` without counting a failed attempt.
- Request and poll answers carry `expiresInSeconds`, the lifetime left on the server's clock, so a
  device with a wrong clock still times the code correctly.
- The device name is untrusted: trimmed, at most 64 characters, no control or invisible formatting
  characters.
- The remote-enrollment setting and the Viewer pairing-code switch (CORE-0393) apply at request,
  regenerate, approval and collection, with the existing invalid-code answer. A regenerated
  request counts as on the LAN only if both the original request and the regenerate were.
- Wire contract 2.11.0 is published through `make publish-wire-contract`; it adds only the seven
  management routes. The registry keeps 2.10.0 active until consumers re-vendor.
- `docs/api/lan-pairing.md` documents the routes for client authors.

## Non-goals

- No change to `/api/pairing-codes` or `/api/enroll`.
- No persistence: a restart drops pending requests and the device asks again.
- Device-requested roles, platform and LAN origin in the pending list are CORE-2063.
- No Console or client UI; that is client work.

## Failure modes

- Unapproved, expired, denied, regenerated-away and reused requests never enroll.
- The display code cannot poll and never redeems through `/api/enroll`.
- A failed build binding on collection is logged and does not withhold the credential, as on
  `/api/enroll`; the device can rebind at handshake.
- If the profile is disabled or deleted between approval and collection, collection fails and the
  request is spent; the device asks again.
- A transient enrollment error at collection does not count a failed attempt, so a device that
  keeps polling through it is not throttled.
- Cancel does nothing to a request whose approval a poll is collecting.

## Acceptance evidence

- `internal/pairing` tests: approval gating, admin-only decisions, expiry before and after approval,
  regenerate and its gates, cancel, deny, single use, the separate throttle, caps that spend no
  attempt, concurrent polls of one approval, Viewer switch, remote gate, name validation.
- `internal/server` tests: the routes, status codes, the credential working on `/api/manifest`,
  and the request code refused by `/api/enroll`.
- Conformance `WireContract|Mgmt|Route`, untagged and App Store tagged.
- A curl transcript against a built server (not tracked; core does not keep agent evidence).

## Rollback

Revert the commit. Pending requests are in memory only, so no stored state changes. Devices
already enrolled through the flow are ordinary devices and stay enrolled. Wire 2.11.0 stays
published but inactive; consumers keep pinning 2.10.0.
