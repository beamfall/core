# Reuse a gate step's prior pass when its inputs are identical

Ticket: CORE-0984 (`queue:core:main`). Related: CORE-0719 (resume a gate run by position).
Status: proposed.

## Why

`make gate` reruns every step on every invocation. A regate on a tree that already passed (after a
landing, after an infrastructure failure in a later step, or on a resubmit) repeats about twenty
minutes of work that cannot produce a different answer.

## What Changes

- `tools/gate/gate.sh` records each pass of a listed step under a key derived from the step name,
  its command, the gate mode, the content of the whole worktree, the toolchain and the
  gate-relevant environment. A later run with the same key reports `REUSE` and does not run the step.
- Records live in the repository's common git directory (`gate-step-reuse/`), so linked worktrees
  share them. They are untracked local state and are never committed.
- The evidence file gets a `reuse` row that names the run that produced the pass. A gate PASS that
  contains reused steps is a PASS for the same tree content; this is the receipt policy.
- Reuse is on by default in normal mode, off in release mode, and `GATE_STEP_REUSE=0` turns it off.
- `tools/gate/gate_step_reuse_test.sh` proves the behaviour and runs with the gate's support tests.

## Non-goals

- Narrowing a step's inputs below the whole worktree. Go tests read scripts, docs and fixtures
  outside their packages, so that needs dependency evidence and is a separate change.
- Reusing steps that depend on state outside the worktree (vulnerability data, sibling checkouts,
  the secret scan, fuzzing, performance budgets).
- Resuming a failed run from a named step (CORE-0719).
