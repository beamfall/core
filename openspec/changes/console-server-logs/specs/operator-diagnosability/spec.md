## ADDED Requirements

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
