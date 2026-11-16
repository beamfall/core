# Bind play queue writes to the Profile they were made for

Ticket: CORE-2177 (`queue:core:main`).

## Why

`switchProfile` rebinds the same session to another Profile. Play queue mutations write to
whatever Profile the session holds when the server processes the request, so a write a client
made for Profile A that is delayed or retried after the switch lands in Profile B's Play Queue.
Clients can refuse to send on another scope and refuse to rebind while a write is unsettled
(CORE-1770), but they cannot recall a write already on the wire. Only the server can refuse it.

## What Changes

- `queueAdd`, `queueRemove`, `queueMove`, `queueClear` and `stationStart` accept an optional
  `expectedProfileID` string argument.
- When it is present and is not the Profile the session is bound to when the write applies, the
  server changes no queue and answers `CONFLICT` with `extensions.reason`
  `PROFILE_MISMATCH`.
- The check and the write are atomic with respect to a Profile rebind of the session: the store
  holds rebinds of every session off from the session check until the queue write has committed.
- When the argument is absent, behaviour is unchanged, so older clients keep working.
- Wire contract `client-core-wire` 2.15.0 publishes the argument and the reason.

## Not in this change

- The Apple and Android clients sending the argument (their own tickets). Once they do, the
  client-side refusal to switch Profile with an unsettled write (CORE-1770) can be dropped.
- A session or Profile generation token. An A to B to A switch while a write is in flight
  passes the check and the write lands in Profile A, the Profile it was made for.
