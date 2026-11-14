# operator-diagnosability Specification

## Purpose
Keeps the server access log useful to an operator by letting each line's level follow the request outcome.

## Requirements

### Requirement: The access line's level follows the outcome

The server SHALL emit a request's `http request` record at Warn when the response took one second
or longer to start, otherwise at Info when the response status is 500 or higher, and otherwise at
Debug. The record SHALL carry `first_byte_ms`, the time from the request's arrival to the start of
its response. The request counters SHALL count every request whatever the log level.

#### Scenario: Routine request at the default level

- **WHEN** a client calls `GET /health`, it is answered 200 at once and the log level is Info
- **THEN** no `http request` record is written and the request is counted on `/metrics`

#### Scenario: Routine request at debug level

- **WHEN** the same request is made with the log level at Debug
- **THEN** one `http request` record is written at Debug

#### Scenario: Server error

- **WHEN** a request is answered 500
- **THEN** its `http request` record is written at Info

#### Scenario: Slow request

- **WHEN** a response takes one second or longer to start
- **THEN** its `http request` record is written at Warn

#### Scenario: Long response that started promptly

- **WHEN** a response starts within one second and then streams for an hour
- **THEN** its `http request` record is written at Debug

#### Scenario: Abandoned request

- **WHEN** the client abandons a request and it is recorded as status 499
- **THEN** its `http request` record is written at Debug
