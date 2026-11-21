# jellyfin-compat-console Specification

## Purpose
Give operators a console control for the Jellyfin compatibility route group (ADR-0171): a default-off toggle that applies without a restart, a per-library allowlist that never admits a Reveal-restricted library, trust-zone CIDR settings and a per-Profile integration sign-in opt-in, none of which ships in App Store binaries.

## Requirements

### Requirement: Compatibility mode is an operator toggle that defaults off

The server SHALL keep the Jellyfin compatibility settings (enabled, allowed CIDRs, trusted proxy
CIDRs, library allowlist) in server configuration. A setting that was never written SHALL fall back
to the bootstrap configuration, whose default is off. A change SHALL apply to the next request
without a restart. While compatibility mode is off, every route under the compatibility prefix SHALL
return 404.

#### Scenario: Operator turns compatibility mode off

- **WHEN** an admin sets compatibility mode to off while it is serving requests
- **THEN** the next request to any route under the compatibility prefix returns 404 without a restart

#### Scenario: Fresh server

- **WHEN** no compatibility setting has been written and the bootstrap configuration does not enable it
- **THEN** compatibility mode is off

### Requirement: Compatibility trust zones are private networks only

The server SHALL accept an allowed or trusted proxy CIDR only when it parses and lies wholly inside
loopback, RFC 1918 private, link-local or IPv6 unique-local address space. It SHALL refuse any other
range, including 100.64.0.0/10, with a validation error and leave the stored settings unchanged.

#### Scenario: Operator submits the overlay range

- **WHEN** an admin submits 100.64.0.0/10 as an allowed CIDR
- **THEN** the server refuses the change and the stored settings are unchanged

### Requirement: Only unrestricted libraries can be allowlisted

The server SHALL accept a library allowlist only when every ID names an existing, non-trashed
library that is not Reveal-restricted, and SHALL refuse the whole change otherwise. The settings
query SHALL list only libraries that are not Reveal-restricted. A compatibility refresh SHALL treat a
library as eligible only when it is allowlisted, exists, and is not Reveal-restricted at refresh
time.

#### Scenario: Restricted library submitted directly

- **WHEN** an admin submits the ID of a Reveal-restricted library in the allowlist
- **THEN** the server refuses the change

#### Scenario: Library restricted after allowlisting

- **WHEN** an allowlisted library is later Reveal-restricted and a compatibility refresh names its path
- **THEN** the library is not refreshed

### Requirement: Integration sign-in is a per-Profile opt-in

The server SHALL let an admin turn integration sign-in on only for an adult, enabled Profile.
Turning it off SHALL revoke that Profile's integration sessions.

#### Scenario: Opt-in for a child Profile

- **WHEN** an admin turns integration sign-in on for a non-adult Profile
- **THEN** the server refuses the change

#### Scenario: Opt-in withdrawn

- **WHEN** an admin turns integration sign-in off for a Profile with an active integration session
- **THEN** that session no longer authenticates compatibility requests

### Requirement: App Store binaries carry no compatibility surface

An App Store build SHALL NOT advertise or dispatch the compatibility settings graph fields, SHALL
serve 404 for every route under the compatibility prefix whatever the stored settings say, and its
Console host SHALL NOT show the compatibility page. Every compatibility settings surface SHALL show a
statement that Beamfall is not affiliated with or endorsed by the Jellyfin project.

#### Scenario: App Store build with compatibility stored as on

- **WHEN** an App Store build starts with compatibility mode stored as on
- **THEN** every route under the compatibility prefix returns 404 and the settings fields are unknown to the graph
