# gate-capacity Specification

## Purpose
Let a gate that is started from inside another gate reuse the capacity its parent already holds, so nested runs do not deadlock on the capacity lock, and refuse an inherited descriptor that does not hold the lock it names.

## Requirements

### Requirement: A nested gate adopts inherited gate capacity

A canonical gate started under a process that holds this repository's gate capacity lock SHALL
adopt that holder's lock through the descriptor named in `BEAMFALL_GATE_CAPACITY_FD` instead of
queueing for a new lock. `script/gate-fixture-guard.py` SHALL keep that descriptor open for the
command it runs.

#### Scenario: Evidence-wrapped gate

- **WHEN** `script/run-with-evidence.sh -- make gate` holds repository capacity and `make gate`
  starts the canonical coordinator through `tools/gate/gate-entry.sh` and the fixture guard
- **THEN** the coordinator adopts the inherited descriptor, the gate body runs once and completes,
  and no descendant process remains

#### Scenario: Holder above the wrapper

- **WHEN** another process already holds repository capacity when the evidence-wrapped gate starts
- **THEN** the evidence wrapper queues behind it and the gate body does not start until it is
  released

#### Scenario: Direct gate

- **WHEN** `make gate` runs with no capacity holder above it
- **THEN** the coordinator opens and acquires the repository capacity lock itself

#### Scenario: Stale descriptor name at the fixture guard

- **WHEN** `BEAMFALL_GATE_CAPACITY_FD` names a closed descriptor when the fixture guard starts
- **THEN** the guard removes the variable from its command's environment

### Requirement: An inherited descriptor must hold the lock it names

The canonical coordinator SHALL refuse, with exit status 70, a `BEAMFALL_GATE_CAPACITY_FD` value
that is not a decimal number, or that names a descriptor referring to this repository's capacity
lock file without holding its exclusive lock. It SHALL ignore a descriptor that refers to a
different file or is closed, and take the repository capacity lock itself.

#### Scenario: Descriptor that does not own the lock

- **WHEN** `BEAMFALL_GATE_CAPACITY_FD` refers to this repository's capacity lock file while
  another description of that file holds the lock
- **THEN** the coordinator exits 70 without starting the gate body

#### Scenario: Foreign or closed descriptor

- **WHEN** `BEAMFALL_GATE_CAPACITY_FD` refers to another repository's lock file or to a closed
  descriptor
- **THEN** the coordinator ignores it, acquires this repository's capacity lock and runs the gate
  body
