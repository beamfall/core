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

### Requirement: Recent server log lines are kept redacted in memory

The server SHALL keep its most recent operator log lines in a bounded in-memory tail beside the
stderr log, and SHALL redact each line before it is kept: known secret classes, the value of any
attribute whose key names a secret, the user name in a home directory path, and every IPv4 or IPv6
address. Lines longer than the per-line bound SHALL be truncated. Stderr output SHALL be unchanged.

#### Scenario: A line with a token and an address

- **WHEN** the server logs a line carrying `token=abc123` and `remote=192.0.2.10:51234`
- **THEN** the kept line reads `token=[REDACTED]` and `remote=[IP]`, and stderr still has the
  original line

#### Scenario: The tail is full

- **WHEN** more lines are written than the tail keeps
- **THEN** only the newest lines are kept, oldest first

### Requirement: An admin reads and downloads the server log from the console

The `serverLogs` query SHALL require an Admin Session and SHALL return the kept lines oldest first,
at most `limit` of them, with the tail's capacity. With no tail it SHALL report `available` false
and no lines. The console Server logs page SHALL show the lines and download them as a text bundle.

#### Scenario: Owner downloads a bundle

- **WHEN** an owner with an Admin Session opens Server logs and chooses Download log bundle
- **THEN** a text file with a short header and the same redacted lines is saved

#### Scenario: No Admin Session

- **WHEN** a manager session without an Admin Session queries `serverLogs`
- **THEN** the request fails with `admin session required`

### Requirement: The log level can change while the server runs

On SIGHUP the server SHALL re-read `<data dir>/log-level` and apply the level it names to the
operator logger without a restart. A missing file SHALL restore the level configured at start-up.
An unreadable file or a value that is not a log level SHALL be logged at Warn and SHALL leave the
current level unchanged. Every change SHALL be logged at Warn with the old level, the new level
and its source. SIGHUP SHALL NOT terminate the server.

#### Scenario: Raise the level to debug

- **WHEN** the server runs at Info, the operator writes `debug` to `<data dir>/log-level` and sends SIGHUP
- **THEN** Debug records, including every `http request` line, are written from then on
- **AND** a Warn record reports the change from INFO to DEBUG

#### Scenario: Remove the file

- **WHEN** the operator deletes `<data dir>/log-level` and sends SIGHUP
- **THEN** the server returns to the level it was started with

#### Scenario: Invalid value

- **WHEN** `<data dir>/log-level` contains `loud` and the operator sends SIGHUP
- **THEN** a Warn record names the invalid value and the level does not change
