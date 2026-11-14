# Reuse preflight passes in the full gate, and one machine-wide gate slot

Tickets: CORE-1569 and CORE-1470 (queue:core:main). One stack, one gate.

## Why

A full gate that follows a green `make gate-preflight` on the same head reruns every step the
preflight just passed. The reuse key carries `preflight=` and the `GATE_PREFLIGHT` variable, the
full gate alone carries `BEAMFALL_GATE_CAPACITY_FD`, and only two of the 55 preflight members are
on the reuse list at all. On the PR #70 landing the gate spent 369 of its 689 step seconds on six
steps the preflight had passed minutes earlier, 262 of them in `support script tests`.

The Mac runs one gate at a time, and nothing enforces that across repositories. Each repository
has its own capacity lock, so sessions wait with a 20-second `pgrep` poll that has no order, also
matches preflights, and lets two sessions start in the same gap.

## What Changes

- **Cross-mode reuse.** The reuse key no longer carries the preflight mode, and the variables that
  only describe how the coordinator was entered (`GATE_PREFLIGHT`, `BEAMFALL_GATE_CAPACITY_FD`,
  `BEAMFALL_GATE_CAPACITY_HELD`, `BEAMFALL_GATE_SLOT_*`) are left out of it. A step whose command
  is the same in both modes is reused in either direction. A step whose command differs gets a
  different key from the command text, and a step whose preflight variant narrows under the same
  command text must be declared mode-bound, which keeps the mode in its key. No step is mode-bound
  today: `tools/gate/gate_scope_test.sh` already proves each preflight member runs the gate's own
  command.
- **Two new reuse classes.** `source` covers steps that read only the worktree and the toolchain,
  so the built web bundle, installed web dependencies and browser set are left out of their key and
  a preflight on a fresh worktree (no bundle yet) still feeds the gate. `source+index` adds the
  staged tree for steps that read the Git index.
- **Six more reusable steps**, each with its soundness argument in `tools/gate/gate.sh`:
  `support script tests`, `golangci-lint (staticcheck + gosec)`, `gate scope selftest` and
  `compile budget manifest selftest` as `source`; `secret scan selftest` and
  `AGPL forbidden-symbol gate` as `source+index`.
- **Steps that stay unreusable are listed with the reason**: they read sibling checkouts, Git
  history, the host's Docker or the diff, or they cost less than the lookup.
- **The record names the producing run's kind**, so a full gate's `REUSE` line and evidence row
  say the pass came from a `make-gate-preflight` run.
- **One machine-wide gate slot.** `script/gate-slot.py` keeps a first-come queue under
  `~/.beamfall/gate-slot` (`BEAMFALL_GATE_SLOT_DIR` overrides it). The head of the queue holds the
  slot; everyone else prints the holder (repository, head, pid, elapsed time, command) and their
  own position, at once and then every 30 seconds. A holder or waiter whose process is gone is
  removed by pid and start time, not by a timeout. `--no-wait` refuses with `GATE_SLOT_BUSY`
  instead of queueing.
- **Core's full gate takes the slot** in the coordinator, before repository capacity, so its place
  in the queue is its arrival time and a superseded or cancelled run leaves the queue. A gate
  started under a holder (a nested gate, or `gate-slot.py -- corvint-tasks gate run ...`) re-enters
  instead of queueing behind its own ancestor.
- **Preflights stay out of the slot.** They keep their own lock (CORE-1321) and print one line
  naming the slot holder when there is one, so the caller can move to `script/wsl-gate.sh`.

## Non-goals

- Changing any other repository's gate entry. `gate-slot.py -- COMMAND` already serializes any
  command from any checkout; apple, apple-ui, android, kit, web and the plugin repositories adopt
  it in their own tickets, as does running the apple gates' cheap checks first.
- Sharing reuse records between machines. Records live under the common Git directory, so a
  preflight on `wsl-lanes` saves nothing on the Mac.
- Holding the slot through the pre-coordinator checks in `tools/gate/gate-entry.sh` (the seeded-red
  canary controls and freshness proofs). They run before the coordinator, as they do for capacity.
- Raising the Corvint Tasks gate timeout. A queue wait inside `make gate` counts against it; wrap
  `corvint-tasks gate run` in `gate-slot.py` to wait outside it.
