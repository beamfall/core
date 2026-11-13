# Run gate preflight outside full-gate capacity, and announce every lock wait

Ticket: CORE-1321 (`queue:core:main`). Lands with CORE-1299.

## Why

`make gate-preflight` (`GATE_PREFLIGHT=1 tools/gate/gate-entry.sh`) runs through the canonical
coordinator `tools/gate/gate_latest.py`, which queued it on the repository-wide gate capacity lock
(`<git-common-dir>/beamfall-gate.capacity.lock`) exactly like a full `make gate`. On 2026-11-12 a
preflight sat silent for 15 minutes behind a full receipted gate and was killed. Preflight exists to
give a seconds-to-minutes verdict before a ~37-minute gate slot; queued behind that slot it costs
the slot it was meant to save, and the silence made the wait indistinguishable from a hang.

Preflight also shared the full gate's latest-wins generation in its worktree, so starting a
preflight in a worktree whose full gate was running superseded (cancelled) that gate.

## What Changes

- With `GATE_PREFLIGHT=1`, `tools/gate/gate_latest.py` does not open or wait on the gate capacity
  lock. It serializes preflights on their own repository-wide lock,
  `<git-common-dir>/beamfall-gate.preflight.lock`, and keeps its generation file and run lock in a
  `preflight/` subdirectory of the coordinator state directory, apart from full gates.
- The preflight body does not inherit `BEAMFALL_GATE_CAPACITY_FD`, and gets
  `BEAMFALL_GATE_CAPACITY_HELD=1` so `tools/gate/gate.sh` does not take capacity itself.
- Whenever the coordinator blocks on a lock (capacity, preflight or the worktree run lock) it prints
  `gate: run N waiting for <lock> held by <holder> (Ns so far)` on stderr at once and every 30
  seconds, and `acquired ... after Ns` when it gets the lock. The holder is the
  `pid P gate run N in <worktree>` record the owning coordinator writes into the lock file; a
  holder that wrote no live record is reported as unrecorded.
- The Makefile comment on `gate-preflight` still describes the old shared-capacity coordination.
  CORE-1326 updates it. This change cannot touch Makefile while CORE-1305's live attempt holds it.

## Non-goals

- Changing the full gate's capacity rules, CORE-0392 capacity inheritance, or the steps preflight
  runs.
- Recording holders from `script/gate-capacity.py`.
