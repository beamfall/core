# Let a nested canonical gate adopt its ancestor's gate capacity

Ticket: CORE-0392 (`queue:core:main`).

## Why

`script/run-with-evidence.sh -- make gate` takes repository gate capacity through
`script/gate-capacity.py`, which holds an exclusive `flock` on
`<git-common-dir>/beamfall-gate.capacity.lock` and names the holding file descriptor in
`BEAMFALL_GATE_CAPACITY_FD`. `make gate` then runs `tools/gate/gate-entry.sh`, which starts the
canonical coordinator `tools/gate/gate_latest.py` under `script/gate-fixture-guard.py`. The guard
closed the inherited descriptor and the coordinator always opened a fresh description of the same
lock file, so it waited forever on the lock its own ancestor held.

`script/gate-capacity.py` (`inherited_lock`) and `script/test-process-supervisor.py` already honour
`BEAMFALL_GATE_CAPACITY_FD`, but that inheritance rule was written down only in code. This change
states it as a requirement and extends it to the fixture guard and the canonical coordinator.

## What Changes

- `script/gate-fixture-guard.py` keeps a descriptor named in `BEAMFALL_GATE_CAPACITY_FD` open for
  the command it runs. A name that refers to a closed descriptor is removed from the command's
  environment; a non-numeric value is passed through unchanged.
- `tools/gate/gate_latest.py` checks `BEAMFALL_GATE_CAPACITY_FD` before opening the capacity lock:
  - a descriptor that refers to this repository's capacity lock file and already holds its
    exclusive lock is adopted (duplicated) instead of queueing for a new lock;
  - a descriptor that refers to this repository's capacity lock file without holding the lock, or a
    non-numeric value, is refused: the coordinator prints `gate coordinator: ...` and exits 70;
  - a descriptor that refers to another file (another repository's lock), a closed descriptor, or
    an unset variable is ignored, and the coordinator opens and waits on the lock as before.
- After adopting, the coordinator keeps its existing latest-wins handling through the generation
  file and the worktree run lock, and names its own descriptor in `BEAMFALL_GATE_CAPACITY_FD` for
  the gate body.
- A direct `make gate`, with no capacity holder above it, still takes repository capacity itself.

## Non-goals

- Changing `script/gate-capacity.py` or `script/run-with-evidence.sh`.
- Changing gate steps, fixture-guard checks or the latest-wins rules.
