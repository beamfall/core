# webhook-delivery Specification

## Purpose
Bound where webhook deliveries can go: endpoint URLs are validated before delivery, disallowed address classes are refused, and a delivery never follows a redirect.

## Requirements

### Requirement: Webhook deliveries never follow redirects

The Webhook Emitter's default delivery client SHALL NOT follow an HTTP redirect. A `3xx` response
SHALL count as a failed delivery attempt, retried within the configured attempt budget and then
dead-lettered.

#### Scenario: Endpoint answers 307 or 308

- **WHEN** a configured endpoint answers a delivery with `307` or `308` and a `Location` on another
  origin
- **THEN** the emitter sends no request to that location, and the event body and signature reach
  only the configured endpoint

#### Scenario: Redirect exhausts the attempt budget

- **WHEN** every attempt for an event is answered with a redirect
- **THEN** the delivery is dead-lettered after the configured number of attempts

### Requirement: Webhook delivery address classes

The default delivery client SHALL check every address it connects to and SHALL NOT use
environment proxies. It SHALL refuse never-routable and IPv4-embedding IPv6 addresses for every
endpoint, and SHALL refuse local addresses unless the endpoint is approved for the local network.

#### Scenario: Hostname endpoint resolves to a local address

- **WHEN** an endpoint configured by hostname without `allow_private_network` resolves to a
  loopback (`127.0.0.0/8`, `::1`), private (`10/8`, `172.16/12`, `192.168/16`, `fc00::/7`),
  shared (`100.64.0.0/10`) or IPv6 site-local (`fec0::/10`) address
- **THEN** the connection is refused and nothing is sent

#### Scenario: Operator-approved LAN receiver

- **WHEN** an endpoint's URL is a private or loopback IP literal, or its configuration sets
  `allow_private_network`
- **THEN** deliveries to that local address succeed

#### Scenario: Cloud metadata address

- **WHEN** an endpoint resolves to a link-local address such as `169.254.169.254`
- **THEN** the connection is refused even when `allow_private_network` is set

#### Scenario: Never-routable address

- **WHEN** an endpoint resolves to an unspecified, `0.0.0.0/8`, multicast or broadcast address, or
  to any other special-purpose block the scraper's public-routability predicate refuses, including
  `192.0.0.0/24`, the documentation ranges (`192.0.2.0/24`, `198.51.100.0/24`, `203.0.113.0/24`,
  `2001:db8::/32`, `3fff::/20`), benchmarking (`198.18.0.0/15`) and reserved (`240.0.0.0/4`)
- **THEN** the connection is refused even when `allow_private_network` is set

#### Scenario: IPv6 form carrying an IPv4 address

- **WHEN** an endpoint resolves to a NAT64 (`64:ff9b::/96`, `64:ff9b:1::/48`), 6to4
  (`2002::/16`), IPv4-compatible (`::/96`, other than `::` and `::1`) or Teredo (`2001::/32`)
  address
- **THEN** the connection is refused whatever IPv4 address it embeds, even when
  `allow_private_network` is set

### Requirement: Webhook endpoint URLs are validated before delivery

The emitter SHALL reject an endpoint at construction, before any delivery attempt, when its URL
does not parse, is not an absolute `http` or `https` URL with a host, carries user info, or names
an IP literal in an always-refused class. The error SHALL be the fixed configuration diagnostic and
SHALL NOT contain the URL.

#### Scenario: Unsafe endpoint URL

- **WHEN** an endpoint URL uses another scheme, has no host, embeds credentials, or names a
  link-local or unspecified IP literal
- **THEN** constructing the emitter fails with the configuration diagnostic and no request is sent

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
