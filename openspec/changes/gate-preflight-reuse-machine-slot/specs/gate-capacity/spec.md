## ADDED Requirements

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
`script/run-with-evidence.sh` SHALL take the slot before capacity for `make gate`. A coordinator
that inherited capacity from a process that does not hold the slot SHALL say so and run without
waiting for the slot.

#### Scenario: Evidence-wrapped gate

- **WHEN** `script/run-with-evidence.sh -- make gate` starts while another gate holds the slot
- **THEN** it waits for the slot and repository capacity stays free until it has the slot

#### Scenario: Capacity taken first

- **WHEN** a full gate starts under a process that holds repository capacity but not the slot,
  while another gate holds the slot
- **THEN** the gate runs without waiting and prints that it did not take the slot
