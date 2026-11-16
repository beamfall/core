## ADDED Requirements

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
