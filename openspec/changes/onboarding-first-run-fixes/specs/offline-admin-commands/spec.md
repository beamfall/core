## ADDED Requirements

### Requirement: Offline commands act on the server's data directory

`beamfall pairing-code` and `beamfall reset-admin-password` SHALL resolve a relative or default
`--data-dir` to the same absolute directory the server uses, and SHALL refuse a directory that holds
no Beamfall database, creating nothing there.

#### Scenario: Mistyped data directory

- **WHEN** either command runs with a `--data-dir` that has no Beamfall database
- **THEN** it exits with an error naming the directory and no database or files are created

#### Scenario: Relative data directory

- **WHEN** the command runs with a relative `--data-dir` that holds a database
- **THEN** it opens that database

### Requirement: pairing-code prints a usable instruction

`beamfall pairing-code` SHALL print the code, the `/web/` URL to open, and the expiry as a duration
and local wall-clock time, with store log lines below warning level suppressed. A wildcard listen
address SHALL print the machine's first LAN IPv4 address, with loopback as the fallback. The URL is
set by `--addr` or `BEAMFALL_ADDR`.

#### Scenario: Default output

- **WHEN** the command mints a code
- **THEN** its output is the code followed by one line naming the URL and the expiry, with no
  migration or "store opened" lines
