# Runtime shutdown: ownership and order

One contract governs retiring the running server. It exists because the pieces
have different owners — HTTP listeners, node-facing session handlers, job
claimers, long-lived workers, and out-of-process children — and a partial order
between them is what produces a listener, a connection, or a worker that
outlives cancellation.

## Ownership

| Resource | Owner | Retired by |
|---|---|---|
| Signal disposition | `cmd/beamfall.main` | `signal.NotifyContext`; the default disposition is restored after the first INT/TERM, so a second one is an escape rather than being swallowed |
| Bound HTTP listener + its connections | `App.Run` | `drainHTTPServer` (graceful window, then an outright close) |
| Relay carrier backend server | its own goroutine under `jobsCtx` | `bsrv.Shutdown` on `jobsCtx.Done` |
| Node-facing session listener | its own goroutine under `jobsCtx` | listener close, then `nodehostd.Server.Shutdown` |
| Node-facing per-connection handlers | `nodehostd.Server` | `nodehostd.Server.Shutdown` (closes every live connection, then joins the handlers) |
| Job claimers | `App.Run` via `claimsCtx` | `stopClaims`, then the graceful drain window |
| Jobs, playback, webhook, scrobble, integration workers | `App.Run` via `jobsCtx` | `cancelJobs`, then a bounded join of the worker group |
| Watcher, playback sessions, archive root | `App` | `checkpointRuntimeState`, while the store is still open |
| Store | `App` | `store.Close`, last |

`App.Run` is fenced to one owner: it binds the listener, closes the address
latch, and closes the store, so a second `Run` is refused rather than allowed to
double-own them. Every close it drives is idempotent, so `Close` after `Run` —
the in-process harness pattern — replays harmlessly.

## Order

`drainHTTPServer` runs first, on the `ctx.Done` path in `App.Run`: no request may
enter while jobs are being joined. `shutdownSequence.run` then executes, in this
order and no other:

1. `stopClaims` — background claim acceptance ends.
2. graceful drain window — an in-flight job handler finishes its own durable
   bookkeeping, bounded by `defaultJobDrainGracePeriod`.
3. `cancelJobs` — `jobsCtx` is cancelled.
4. bounded worker join — the worker group is joined, bounded by
   `workerJoinBound`.
5. `checkpoint` — runtime state is checkpointed while the store is still open.
6. `closeStore` — last, so no worker write can race a closing store.

## Boundedness

Every wait is bounded, because an unbounded one converts a stalled subsystem
into a process that never exits and never releases its port:

- `httpDrainBound` caps the graceful HTTP drain. A media byte route streams
  without a write deadline, so waiting for one to finish is unbounded by
  construction; a connection still live at the bound is closed outright. The
  forced close is an ordinary outcome, logged, not an error.
- `workerJoinBound` caps the worker join. Past it, `errWorkerJoinTimedOut` is
  reported and phases 5 and 6 still run — the durable checkpoint and the store
  close are what an operator loses when shutdown stalls.
- `nodehostd.handshakeTimeout` caps a single node connection exchange, and
  `nodehostd.Server.Shutdown` does not wait it out: it closes every live
  connection first, then joins.

No bound is implemented with a sleep. Each is a deadline or a timer race against
the real completion signal.
