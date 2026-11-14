# gate-step-reuse Specification

## Purpose
Let the local gate skip a listed step when its recorded inputs are identical to a recorded pass, so a repeat gate is fast, without ever passing a step whose inputs changed or whose record cannot be trusted.

## Requirements

### Requirement: Reuse on identical inputs

The gate SHALL skip a listed step and report it as reused when a recorded pass exists for the same
step name, command, normal or release mode, worktree content, toolchain and gate-relevant
environment. Whether the pass came from a preflight or a full gate SHALL NOT be part of the match,
except for a step declared mode-bound.

#### Scenario: Unchanged tree

- **WHEN** a listed step passed and the gate runs again with nothing changed
- **THEN** the step is not executed, the output shows `REUSE` with the source run, and the evidence
  file carries a `reuse` row for the step

#### Scenario: Another worktree with the same content

- **WHEN** a linked worktree of the same repository holds identical content
- **THEN** the pass recorded from the first worktree is reused

#### Scenario: Full gate after a green preflight

- **WHEN** `make gate-preflight` passed a listed step and `make gate` then runs on the same content
  with the same command for that step
- **THEN** the full gate does not execute the step, and its `REUSE` line and evidence row name the
  preflight run and its kind `make-gate-preflight`

#### Scenario: Preflight after a full gate

- **WHEN** a full gate passed a listed step and a preflight then runs on the same content
- **THEN** the preflight reuses the pass

### Requirement: Any input change runs the step

The gate SHALL run a step when any tracked or untracked non-ignored file, the step command, normal
or release mode, the toolchain version or a gate-relevant environment variable differs from the
recorded pass. Variables that only describe how the coordinator was entered (`GATE_PREFLIGHT`,
`BEAMFALL_GATE_CAPACITY_FD`, `BEAMFALL_GATE_CAPACITY_HELD`, `BEAMFALL_GATE_SLOT_*`) are not
gate-relevant.

#### Scenario: Edited or added file

- **WHEN** a tracked file is edited or an untracked non-ignored file is added
- **THEN** every listed step runs

#### Scenario: Base-dependent step

- **WHEN** a step whose result depends on the diff base runs against a different base tree, or the
  base cannot be resolved
- **THEN** the step runs

#### Scenario: Command differs between preflight and gate

- **WHEN** a step of the same name runs a different command in the preflight than in the full gate
- **THEN** neither mode reuses the other's pass

### Requirement: Inputs outside the tracked tree

The gate SHALL include in the key the built web bundle, the installed web dependency set, the
browser and media tools the steps call, and for a diff-scoped step both ends of the diff. It SHALL
run every step when an input outside the worktree cannot be content-addressed. A step classed as
reading only source SHALL leave the built web bundle, the installed web dependency set and the
browser set out of its key, and a step that reads the Git index SHALL add the staged index entries.

#### Scenario: Missing or different web bundle

- **WHEN** the ignored built web bundle is absent or differs from the one in place when the step passed
- **THEN** the step runs

#### Scenario: Step that writes the web bundle

- **WHEN** a step passes and leaves a new web bundle behind
- **THEN** the pass is recorded against that bundle and reused only while it is in place

#### Scenario: Committing an edit a diff-scoped step never saw

- **WHEN** a diff-scoped step passed with an uncommitted edit and the edit is then committed
- **THEN** the step runs

#### Scenario: External test inputs

- **WHEN** `RELAY_VECTORS_PATH` is set or a `go.work` file is active
- **THEN** no step is reused

#### Scenario: Unknown browser directory

- **WHEN** Playwright's browser directory for this host cannot be established
- **THEN** the step that launches a browser runs and is not recorded, and other steps are unaffected

#### Scenario: Source-only step and a new web bundle

- **WHEN** a source-only step passed before the web bundle was built and the bundle is then built
- **THEN** the source-only step is reused, and a step that embeds the bundle runs

#### Scenario: Index-reading step and a staging change

- **WHEN** a step that reads the Git index passed and a file is then staged or unstaged without any
  change to worktree content
- **THEN** the step runs, and the gate has not written the real index

### Requirement: Reuse never weakens the gate

The gate SHALL NOT reuse a result in release mode, for a step outside the reuse list, from a failed
step, or from a pass during which the worktree content changed.

#### Scenario: Release mode

- **WHEN** the gate runs in release mode
- **THEN** every step runs and no record is consulted

#### Scenario: Step that changes the tree

- **WHEN** the worktree content after a step differs from the content before it
- **THEN** the pass is not recorded and the output says so

#### Scenario: Operator opt-out

- **WHEN** `GATE_STEP_REUSE=0` is set
- **THEN** every step runs

### Requirement: Records fail closed

The gate SHALL run the step when a record is missing, malformed, of an unknown format, not a pass,
for a different key, older than the age limit or dated in the future, and SHALL NOT fail because
the record store is unusable.

#### Scenario: Damaged record

- **WHEN** the record for a key is empty, truncated or edited
- **THEN** the step runs

#### Scenario: Unwritable store

- **WHEN** the record directory cannot be created or written
- **THEN** the step runs and its result alone decides the outcome

#### Scenario: Malformed age limit

- **WHEN** `GATE_STEP_REUSE_MAX_AGE_S` is not a plain decimal number of seconds
- **THEN** the step runs, its pass is not recorded, and the gate carries on

### Requirement: A mode-bound step is never reused across modes

The gate SHALL keep the preflight mode in the key of a step declared mode-bound, for a step whose
preflight variant checks less under the same command text. Every step that runs in both modes
without that declaration SHALL run the same command in both.

#### Scenario: Mode-bound step

- **WHEN** a mode-bound step passed in a preflight and the full gate runs on the same content
- **THEN** the full gate runs the step, and a second full gate reuses the full gate's pass

### Requirement: The reuse list states why

Every step on the reuse list SHALL carry, beside the list, the reason its inputs are covered by its
class, and the steps that run in both modes and are left off SHALL be listed with the reason.

#### Scenario: Step that reads outside the worktree

- **WHEN** a step reads sibling checkouts, Git history, the host's Docker or the pull request diff
- **THEN** it is not on the reuse list and runs in every gate
