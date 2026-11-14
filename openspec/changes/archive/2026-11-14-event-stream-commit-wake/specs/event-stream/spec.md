## ADDED Requirements

### Requirement: Streams wake on committed events

The server SHALL wake an open event stream when an outbox write commits in the server process, and
the stream SHALL NOT read the outbox while idle except for one fallback read every 30 seconds.

#### Scenario: Idle stream

- **WHEN** an event stream has drained its backlog and no event is written for one second
- **THEN** the stream performs no outbox read in that second

#### Scenario: Event committed while idle

- **WHEN** an event is committed to the outbox while a permitted stream is idle
- **THEN** the stream delivers it without waiting for a poll interval

#### Scenario: Writer that cannot signal

- **WHEN** an event reaches the outbox without the in-process signal
- **THEN** an idle stream delivers it within 30 seconds, on its fallback read

## MODIFIED Requirements

### Requirement: Idle keepalive

The server SHALL write an SSE comment frame on an open event stream whenever it has written nothing
for 10 seconds, so an idle stream carries a frame at least every 15 seconds, and SHALL end the
stream when that write fails.

#### Scenario: Idle stream

- **WHEN** an event stream is open and the outbox holds no events for the caller
- **THEN** the client receives a `: keepalive` comment frame at least every 15 seconds

#### Scenario: Peer gone

- **WHEN** writing the keepalive frame fails
- **THEN** the handler ends the stream and stops waiting

### Requirement: Cached stream scope

The server SHALL re-derive an event stream's auth scope before it filters any batch of outbox
events, and otherwise at most every 5 seconds, so an outbox read that finds no events performs no
scope derivation.

#### Scenario: Idle poll

- **WHEN** a fallback read finds no events and the cached scope is younger than 5 seconds
- **THEN** the stream performs that one outbox read and no scope derivation

#### Scenario: Revoked device with pending events

- **WHEN** a device is revoked and an event is then appended to the outbox
- **THEN** the revoked device's stream does not deliver that event and ends

#### Scenario: Revoked device while idle

- **WHEN** a device is revoked and no events follow
- **THEN** its stream ends within 5 seconds of the revocation
