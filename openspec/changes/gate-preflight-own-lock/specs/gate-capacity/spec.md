## ADDED Requirements

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
