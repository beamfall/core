## ADDED Requirements

### Requirement: A request can name the Profile it was made for

A request MAY carry the header `X-Beamfall-Profile`. When the header is non-blank and is not the
Profile the request's credential is bound to, the server SHALL refuse the request with HTTP 409,
code `CONFLICT` and reason `PROFILE_MISMATCH`, before the route's handler runs. A request without
the header, or with a blank one, SHALL behave exactly as before.

#### Scenario: A write from a tab that still shows the previous Profile is refused

- **WHEN** a session bound to Profile A is switched to Profile B and a request then arrives with
  `X-Beamfall-Profile` set to Profile A
- **THEN** the reply is HTTP 409 with `CONFLICT` and reason `PROFILE_MISMATCH`
- **AND** neither Profile's state changes

#### Scenario: The refusal takes each route's error shape

- **WHEN** the mismatch is on `/api/graphql`
- **THEN** the error carries code `CONFLICT` and `extensions.reason` `PROFILE_MISMATCH`
- **WHEN** the mismatch is on another route
- **THEN** the body is `{"error": "profile mismatch", "code": "CONFLICT", "reason": "PROFILE_MISMATCH"}`

#### Scenario: A matching header is served

- **WHEN** a session bound to Profile A sends a request with `X-Beamfall-Profile` set to Profile A
- **THEN** the request is served as if the header were absent

#### Scenario: Clients without the header are unaffected

- **WHEN** a request carries no `X-Beamfall-Profile` header
- **THEN** it is served for the credential's current Profile as before

### Requirement: A request stays bound to its Profile until it applies

A request that names a Profile SHALL be refused with the same 409 at every later resolution of its
scope. An event stream naming a Profile SHALL end once its session is bound to another Profile. A
Reveal, relock or playback end SHALL apply only while the session is still bound to the Profile the
request resolved to.

#### Scenario: A switch that lands after the route check still refuses the write

- **WHEN** the session is switched to Profile B after the route's check passed for Profile A
- **THEN** the handler's own scope resolution refuses the request with the same 409
- **AND** a Reveal whose PIN was verified for Profile A does not reveal Profile B

#### Scenario: An event stream for the previous Profile ends

- **WHEN** an event stream naming Profile A is open and the session is switched to Profile B
- **THEN** the stream delivers none of Profile B's events and ends
- **AND** a reconnect naming Profile A is refused with the 409

### Requirement: The web app names its Profile and reloads on a mismatch

The web app SHALL send `X-Beamfall-Profile` with the signed-in Profile on every API request once
it has read that Profile, including on its event stream and keepalive writes, and SHALL send none on
the reads of an identity load. When a request is
refused with `PROFILE_MISMATCH` for the Profile the tab still shows, the web app SHALL start a new
identity epoch and show the Profile the session is now bound to. A refusal for a Profile the tab
no longer shows SHALL be ignored.

#### Scenario: A stale tab catches up instead of writing

- **WHEN** one tab switches the session from Profile A to Profile B and another tab still showing
  Profile A saves a preference
- **THEN** the save does not reach either Profile
- **AND** the second tab reloads and shows Profile B

#### Scenario: A stale tab's Reveal or switch names the Profile it shows

- **WHEN** a tab still showing Profile A sends a Reveal or a Profile switch
- **THEN** the request names Profile A and is refused
