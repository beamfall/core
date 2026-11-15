## ADDED Requirements

### Requirement: Owners manage webhook endpoints from the console

The server SHALL let an admin session list, add, edit, enable, disable, test and remove webhook
endpoints without a restart. Each change SHALL be written to the activity log. An endpoint secret
SHALL be stored only in a `0600` secret file and SHALL NOT be returned by any read; reads report
only whether a secret is set.

#### Scenario: Owner adds an endpoint

- **WHEN** an owner saves a new endpoint with a valid URL, format and event selection
- **THEN** the emitter delivers matching events to it without a restart, and the endpoint list
  reports it with `secretSet` and no secret value

#### Scenario: Invalid endpoint

- **WHEN** an owner saves an endpoint with an invalid URL, an unknown format, or an email format
  without sender and recipient addresses
- **THEN** the mutation fails with a client error and the running configuration is unchanged

#### Scenario: Owner removes an endpoint

- **WHEN** an owner removes an endpoint
- **THEN** no further deliveries are attempted to it and its secret file is deleted

### Requirement: Webhook endpoint formats

Each endpoint SHALL have one format: `generic`, `discord`, `slack`, `ntfy`, `gotify` or `email`.
`generic` SHALL send the existing HMAC-signed event envelope. The other formats SHALL render
webhook events as a human-readable message in the target's own request shape, and SHALL NOT
receive raw event-spine kinds.

#### Scenario: Discord endpoint

- **WHEN** a `media.added` event is delivered to a `discord` endpoint
- **THEN** the request is a Discord webhook execution with an embed carrying the title, the Media
  class and the artwork, attached as a file when artwork is available

#### Scenario: ntfy endpoint

- **WHEN** an event is delivered to an `ntfy` endpoint with a token set
- **THEN** the request carries the title and message in ntfy's headers and body and the token as
  a bearer credential

#### Scenario: Email endpoint

- **WHEN** an event is delivered to an `email` endpoint
- **THEN** a plain-text message is sent over SMTP with STARTTLS or implicit TLS, and a password is
  never sent over an unencrypted connection

### Requirement: Derived webhook events

The emitter SHALL deliver `media.added`, `playback.started`, `playback.stopped`,
`profile.switched`, `admin.signed_in`, `admin.sign_in_failed`, `device.enrolled`,
`device.revoked` and `session.terminated`. It SHALL derive them from the event spine, the Live
Sessions view and the activity log, without adding outbox kinds or SSE wire schema.

#### Scenario: New Media

- **WHEN** a Media row is created in an unrestricted Library
- **THEN** subscribed endpoints receive `media.added` with its title, class and artwork URL

#### Scenario: Playback starts and stops

- **WHEN** a session begins playing a Media and later its stream grant ends
- **THEN** subscribed endpoints receive `playback.started` and then `playback.stopped` naming the
  Profile, device and Media

#### Scenario: Server restart during playback

- **WHEN** the server starts while sessions are already playing
- **THEN** no `playback.started` is delivered for those sessions

### Requirement: Webhook Media events are reveal-safe

Media events SHALL carry the Media title, class and, when a public base URL is set, an artwork
URL. Events about Media in a restricted or Sealed Library SHALL NOT be delivered.

#### Scenario: Restricted Library

- **WHEN** a Media row is created in a restricted Library
- **THEN** no endpoint receives an event for it

### Requirement: Webhook test-send and delivery log

An owner SHALL be able to send a test event to one endpoint and get its outcome. The emitter SHALL
keep a bounded log of recent delivery attempts with endpoint, event, outcome, HTTP status class,
duration and time, and SHALL NOT record secrets, signatures or response bodies in it.

#### Scenario: Test send succeeds

- **WHEN** an owner tests an endpoint whose target answers `2xx`
- **THEN** the mutation reports success and the delivery log gains a test entry

#### Scenario: Test send fails

- **WHEN** an owner tests an endpoint whose target refuses the request
- **THEN** the mutation reports the fixed diagnostic and status class, and no secret or response
  body is returned
