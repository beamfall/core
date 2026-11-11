## ADDED Requirements

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
