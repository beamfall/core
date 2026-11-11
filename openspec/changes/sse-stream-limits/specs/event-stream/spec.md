## ADDED Requirements

### Requirement: Idle keepalive

The server SHALL write an SSE comment frame on an open event stream whenever it has written nothing
for 10 seconds, so an idle stream carries a frame at least every 15 seconds, and SHALL end the
stream when that write fails.

#### Scenario: Idle stream

- **WHEN** an event stream is open and the outbox holds no events for the caller
- **THEN** the client receives a `: keepalive` comment frame at least every 15 seconds

#### Scenario: Peer gone

- **WHEN** writing the keepalive frame fails
- **THEN** the handler ends the stream and stops polling

### Requirement: Per-session stream cap

The server SHALL allow at most 8 concurrently open event streams per session, API token or scoped
key, and SHALL refuse a further stream with `429` before writing stream headers.

#### Scenario: Cap reached

- **WHEN** a session already holds 8 open event streams and opens another
- **THEN** the new request is answered `429` and the existing streams continue

#### Scenario: Slot released

- **WHEN** one of a capped session's streams ends
- **THEN** that session can open a new stream

### Requirement: Cached stream scope

The server SHALL re-derive an event stream's auth scope before it filters any batch of outbox
events, and otherwise at most every 5 seconds, so a poll that finds no events performs at most one
store read.

#### Scenario: Idle poll

- **WHEN** a poll finds no events and the cached scope is younger than 5 seconds
- **THEN** the poll performs one outbox read and no scope derivation

#### Scenario: Revoked device with pending events

- **WHEN** a device is revoked and an event is then appended to the outbox
- **THEN** the revoked device's stream does not deliver that event and ends

#### Scenario: Revoked device while idle

- **WHEN** a device is revoked and no events follow
- **THEN** its stream ends within 5 seconds of the revocation plus one poll

### Requirement: Open-stream gauge

The server SHALL report the number of open event streams on `/metrics` as
`beamfall_events_streams_open`.

#### Scenario: Scrape with open streams

- **WHEN** two event streams are open and an authorized scraper reads `/metrics`
- **THEN** the scrape carries `beamfall_events_streams_open 2`
