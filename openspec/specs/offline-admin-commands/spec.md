# offline-admin-commands Specification

## Purpose
How offline admin commands such as `beamfall pairing-code` and `beamfall reset-admin-password` find the server's data directory, refuse a directory that holds no Beamfall database, and print instructions an operator can act on.

## Requirements

### Requirement: Offline commands act on the server's data directory

`beamfall pairing-code` and `beamfall reset-admin-password` SHALL resolve a relative or default
`--data-dir` to the same absolute directory the server uses, and SHALL refuse a directory that holds
no Beamfall database, creating nothing there.

#### Scenario: Mistyped data directory

- **WHEN** either command runs with a `--data-dir` that has no Beamfall database
- **THEN** it exits with an error naming the directory and no database or files are created

#### Scenario: Empty or foreign database file

- **WHEN** the directory's `db/catalog.db` is empty, is not SQLite, or is an SQLite database (WAL
  mode included) whose migration ledger does not record Beamfall's first catalog migration
- **THEN** the command refuses it after a read-only check that creates no file (no `-wal` or
  `-shm`) and leaves every file unchanged

#### Scenario: Server already running on the directory

- **WHEN** the command runs beside a server whose catalog still holds its migration ledger only in
  the WAL
- **THEN** the read-only check recognises the catalog without creating or changing any file in its
  directory, even if the server opens or closes the database during the check

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

### Requirement: Pairing code names the start step and refuses a running server

`beamfall pairing-code` SHALL refuse to run while a server holds the data directory. Because only a
running server redeems the code, its instruction SHALL tell the operator to start the server before
opening the URL.

#### Scenario: Start step

- **WHEN** the command mints a code
- **THEN** its instruction tells the operator to start the server, then open the URL

#### Scenario: Server already running

- **WHEN** the command runs while a server holds the data directory
- **THEN** it refuses without opening the store
