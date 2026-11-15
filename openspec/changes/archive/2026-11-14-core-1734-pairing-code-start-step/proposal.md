# pairing-code names the start-the-server step

Ticket: CORE-1734 (`queue:core:main`).

## Why

`beamfall pairing-code` refuses to run while a server holds the data directory, because opening the
store runs startup maintenance (scratch sweeps, migrations) that a live server owns. Only a running
server redeems the code, so the operator must start the server after minting it. The CORE-1609
output change dropped that step and told the operator to open the URL straight away.

## What Changes

- The instruction line reads "Start the server, then open … and enter this code …".
- The command keeps refusing a running server, and a test asserts the printed steps.

## Non-goals

- Minting a code beside a running server. That needs the server to mint it, or a store open without
  startup maintenance; it is not part of this fix.
