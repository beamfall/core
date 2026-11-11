## ADDED Requirements

### Requirement: Private feed credentials stay on the feed origin

The Subscription Poller SHALL send a Subscription's private-feed `Authorization` header only to
the normalized origin of the Subscription's feed URL: the same scheme, case-insensitive hostname
and effective port. An enclosure download on any other origin, or with an unparseable URL, SHALL
carry no `Authorization` header.

#### Scenario: Same-origin enclosure

- **WHEN** a private feed lists an enclosure on the feed URL's scheme, host and port
- **THEN** the enclosure download carries the feed credential

#### Scenario: Enclosure on another host or port

- **WHEN** a private feed lists an enclosure whose host or effective port differs from the feed
  URL's
- **THEN** the enclosure is requested without an `Authorization` header

#### Scenario: Same-origin enclosure redirects to another origin

- **WHEN** a same-origin enclosure answers with a redirect to another host or port
- **THEN** the redirect is refused and the other origin receives no request carrying the
  credential
