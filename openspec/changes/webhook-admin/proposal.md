# Manage webhooks from the admin console, with chat formats and richer events

Ticket: CORE-1697 (`queue:core:main`), from the Jellyfin client-parity audit of 2026-11-14.

## Why

Webhooks are configured only by hand-editing `webhook-endpoints.json` and restarting the server.
Every endpoint receives the raw event-spine envelope, so a Discord, Slack, ntfy, Gotify or email
target needs a relay in between. The spine carries Media changes and device signals, but nothing
about playback starting or stopping, sign-ins or devices being added. Owners cannot send a test
delivery or see whether deliveries succeed.

## What Changes

- The Webhook Emitter always runs and reloads its endpoints when the configuration changes. With
  no enabled endpoint it does no work and makes no request.
- Owners add, edit, enable, disable, test and remove endpoints from a new console page
  (Access → Webhooks). The page shows the recent delivery log and each endpoint's last result.
  New admin graph operations back it: `webhooks`, `setWebhook`, `deleteWebhook`, `testWebhook`
  and `setWebhookSettings`. Each mutation writes the activity log.
- Each endpoint has a format: `generic` (the existing HMAC-signed envelope), `discord`, `slack`,
  `ntfy`, `gotify` or `email` (SMTP). The endpoint secret is the format's credential: the HMAC key,
  the ntfy access token, the Gotify application token or the SMTP password. It is written to a
  `0600` file in the secret store and is never returned by the graph.
- New webhook events, derived inside the emitter without adding outbox kinds or wire schema:
  - `media.added`, from the spine's `media.created`, with the Media title, class, year and an
    artwork URL;
  - `playback.started` and `playback.stopped`, from changes in the Live Sessions view;
  - `profile.switched`, `admin.signed_in`, `admin.sign_in_failed`, `device.enrolled`,
    `device.revoked` and `session.terminated`, from the activity log.
- The artwork URL is built from an owner-set public base URL. Discord and ntfy deliveries also
  attach the artwork image, so the picture appears even when the server cannot be reached from
  the internet.
- Events for Media in a restricted or Sealed Library are never delivered.
- A bounded delivery log (endpoint, event, outcome, status class, duration, time) is kept with
  the dead-letter in plugin-private JSON.

## Non-goals

- Automation rules or chained actions (CORE-0375).
- Moving the emitter onto the dispatcher's outbox polling (CORE-1264).
- New outbox kinds or SSE wire changes. The new events exist only for webhook delivery.
- User-editable payload templates. Each format has one fixed template.
- Per-profile notification preferences.

## Failure modes

- A test delivery that fails reports the fixed diagnostic and HTTP status class; the secret and
  the target's response body are never echoed.
- An invalid endpoint (bad URL, unknown format, missing email address) is rejected by the mutation
  and the running configuration is left untouched.
- A Live Sessions stop is seen when the session's stream grant ends, so `playback.stopped` lags a
  client stop by up to that window. Sessions already playing when the server starts produce no
  `playback.started`.
- An SMTP server that offers no STARTTLS is refused unless the endpoint is approved for the local
  network; a password is never sent without TLS.

## Acceptance evidence

- Unit tests in `internal/webhook` cover each format's request, the event derivations, reload,
  test-send, the delivery log and SMTP delivery against a local double.
- Graph tests cover the admin operations and that no secret is returned.
- Vitest covers the console page: add, test, remove and the delivery log.
- Manual check on a running server: add a Discord-format endpoint pointing at a local receiver,
  send a test, and see it in the delivery log.

## Rollback

Revert the change. Endpoints written by the console remain in `webhook-endpoints.json`; the
reverted emitter ignores the new fields and delivers the generic envelope to every endpoint,
including chat targets, which reject it. Remove those endpoints before rolling back.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `webhook-delivery`: endpoint management, formats, derived events, test-send and delivery log.
