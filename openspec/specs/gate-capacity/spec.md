# gate-capacity Specification

## Purpose
Let a gate that is started from inside another gate reuse the capacity its parent already holds, so nested runs do not deadlock on the capacity lock, refuse an inherited descriptor that does not hold the lock it names, and give gate preflight its own lock so it never queues behind a full gate.

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

### Requirement: Gate preflight does not wait on full-gate capacity

The canonical coordinator SHALL run a `GATE_PREFLIGHT=1` gate without opening or waiting on the
repository gate capacity lock. Preflights SHALL serialize on their own repository-wide preflight
lock, and a preflight's latest-wins generation SHALL be separate from the full gate's, so a
preflight never supersedes a full gate in the same worktree.

#### Scenario: Full gate holds capacity

- **WHEN** another process holds repository gate capacity and `make gate-preflight` starts
- **THEN** the preflight body starts and completes while that holder still owns capacity

#### Scenario: Another worktree's preflight is running

- **WHEN** a preflight from another worktree of the same repository holds the preflight lock
- **THEN** the new preflight waits for it, and runs once it is released

### Requirement: A coordinator announces every lock wait

When the canonical coordinator blocks on a lock it SHALL print, on stderr, what it is waiting for
and the holder recorded in the lock file, immediately and then periodically while it waits.

#### Scenario: Queued full gate

- **WHEN** a full gate queues on capacity held by another coordinator
- **THEN** its stderr names the capacity lock and the holder's pid, run number and worktree

#### Scenario: Queued preflight

- **WHEN** a preflight queues on the preflight lock held by another worktree's preflight
- **THEN** its stderr names the preflight lock and the holder's pid, run number and worktree

### Requirement: One full gate at a time on a machine, in arrival order

`script/gate-slot.py -- COMMAND` SHALL run COMMAND only while its caller is at the head of one
per-user queue shared by every repository on the machine, and SHALL grant the slot in the order
callers joined the queue. Once at the head it SHALL become COMMAND, so the slot is held for exactly
as long as COMMAND's own process lives and COMMAND receives its signals directly.

#### Scenario: Two gates

- **WHEN** a second caller starts while the first holds the slot
- **THEN** the second command does not start until the first has ended

#### Scenario: Arrival order

- **WHEN** two callers queue behind a holder, one after the other
- **THEN** the earlier caller runs first

#### Scenario: Command status

- **WHEN** the command a holder ran exits with a status
- **THEN** the caller's exit status is that status and the slot is free

### Requirement: An interrupted waiter leaves the queue

A caller interrupted by SIGINT, SIGTERM or SIGHUP while it waits SHALL leave the queue without
running COMMAND and exit with 128 plus the signal number.

#### Scenario: Cancelled waiter

- **WHEN** a waiting caller receives SIGTERM
- **THEN** it leaves the queue without running its command and exits 143

### Requirement: A waiter says who holds the slot and where it stands

While it waits, a caller SHALL print the holder's repository, head, pid, elapsed time and command
and its own position in the queue, immediately and then periodically. With `--no-wait` or
`BEAMFALL_GATE_SLOT_NOWAIT=1` it SHALL instead stay out of the queue, print `GATE_SLOT_BUSY` with
the same holder description and exit 69.

#### Scenario: Queued caller

- **WHEN** a caller joins the queue behind a holder
- **THEN** its first line names the holder's repository, head and pid and reports queue position 1

#### Scenario: No-wait caller

- **WHEN** a `--no-wait` caller finds the slot held
- **THEN** it exits 69 with `GATE_SLOT_BUSY`, its command does not run and it leaves no queue entry

### Requirement: A dead holder is reclaimed by process liveness

A queue entry SHALL count only while the process that wrote it is alive, decided from its pid and
recorded start time. No timeout SHALL release a live holder.

#### Scenario: Killed holder

- **WHEN** the holder is killed without the chance to leave the queue
- **THEN** the next waiter removes its entry, says so, and takes the slot

#### Scenario: Reused pid

- **WHEN** an entry's pid belongs to a live process with a different start time
- **THEN** the entry is treated as dead

#### Scenario: Different time zones

- **WHEN** the holder and a waiter run with different `TZ` settings
- **THEN** the waiter reads the same start time for the holder and waits for it

### Requirement: A gate under a holder re-enters

A caller whose ancestor process holds the slot SHALL run without joining the queue.

#### Scenario: Nested gate

- **WHEN** a command running under the slot starts another slot-taking gate, with or without the
  holder's environment
- **THEN** the inner gate runs at once and the slot is released only when the outer holder ends

### Requirement: Core's full gate takes the machine slot

The canonical coordinator SHALL take the machine slot for a full gate entered through
`tools/gate/gate-entry.sh`, before repository capacity, and SHALL leave the queue when the run is
cancelled or superseded. A gate preflight SHALL NOT take or wait for the slot; when the slot is
held it SHALL print the holder once. A slot store that cannot be used SHALL NOT fail the gate: the
coordinator says so and runs.

#### Scenario: Another repository's gate holds the slot

- **WHEN** `make gate` starts in core while a gate from another repository holds the slot
- **THEN** core's gate body does not start until that gate ends, and the coordinator prints the
  holder and its position while it waits

#### Scenario: Superseded while queued

- **WHEN** a newer `make gate` starts in the same worktree while the older one is queued for the slot
- **THEN** the older run exits as superseded and its queue entry is gone

#### Scenario: Preflight while the slot is held

- **WHEN** `make gate-preflight` starts while another gate holds the slot
- **THEN** the preflight body starts without waiting and its output names the slot holder

#### Scenario: Unusable slot store

- **WHEN** the slot directory cannot be created or written
- **THEN** the gate prints that it is running without the machine slot and runs

### Requirement: The slot is taken before repository capacity

No gate SHALL wait for the machine slot while it holds repository capacity.
`script/run-with-evidence.sh` SHALL take the slot before capacity for `make gate`, and SHALL NOT
queue for the slot when its caller already holds a capacity lock. A coordinator started under a
process that holds any repository's capacity lock but not the slot SHALL say so and run without
waiting for the slot.

#### Scenario: Evidence-wrapped gate

- **WHEN** `script/run-with-evidence.sh -- make gate` starts while another gate holds the slot
- **THEN** it waits for the slot and repository capacity stays free until it has the slot

#### Scenario: Capacity taken first

- **WHEN** a full gate starts under a process that holds repository capacity but not the slot,
  while another gate holds the slot
- **THEN** the gate runs without waiting and prints that it did not take the slot

#### Scenario: Another repository's capacity

- **WHEN** a full gate starts under a process that holds a different repository's capacity lock
  but not the slot, while another gate holds the slot
- **THEN** the gate runs without waiting and prints that it did not take the slot

#### Scenario: Evidence wrapper under capacity

- **WHEN** `script/run-with-evidence.sh -- make gate` starts under a process that already holds
  repository capacity, while another gate holds the slot
- **THEN** the wrapper does not join the slot queue and the gate runs
