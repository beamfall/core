## MODIFIED Requirements

### Requirement: pairing-code prints a usable instruction

`beamfall pairing-code` SHALL print the code, the `/web/` URL to open, and the expiry as a duration
and local wall-clock time, with store log lines below warning level suppressed. A wildcard listen
address SHALL print the machine's first LAN IPv4 address, with loopback as the fallback. The URL is
set by `--addr` or `BEAMFALL_ADDR`. The command SHALL refuse to run while a server holds the data
directory, and because only a running server redeems the code, the instruction SHALL tell the
operator to start the server before opening the URL.

#### Scenario: Default output

- **WHEN** the command mints a code
- **THEN** its output is the code followed by one line naming the URL and the expiry, with no
  migration or "store opened" lines

#### Scenario: Start step

- **WHEN** the command mints a code
- **THEN** its instruction tells the operator to start the server, then open the URL

#### Scenario: Server already running

- **WHEN** the command runs while a server holds the data directory
- **THEN** it refuses without opening the store
