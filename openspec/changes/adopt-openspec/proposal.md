# Adopt OpenSpec for new changes

Ticket: CORE-0959 (`queue:core:main`).
Status: proposed. Only the owner accepts this intent.

## Why

Work has been dispatched from ticket bodies alone, ahead of any written specification, and the
existing documents then turned out not to describe the current plan. A change proposal that is
committed before the code gives every agent and reviewer one place to read the intended behaviour.

## What Changes

- Add `openspec/` (OpenSpec `spec-driven` schema) with project context in `openspec/config.yaml`.
- Require an OpenSpec change under `openspec/changes/<id>/` before implementation of new or changed
  behaviour. A bug fix against an existing requirement cites that requirement instead.
- Keep Corvint Tasks as the queue of record. Each change names its ticket; `tasks.md` is a checklist.
- Leave existing ADRs and `docs/` pages canonical. A capability gets a spec in `openspec/specs/`
  only when a change touches it.
- Add an `AGENTS.md` section that points agents at the workflow.
- Ignore generated agent tool files (`.agents/`, alongside the already ignored `.claude/`); each
  checkout regenerates them with `openspec update`.

## Non-goals

- Backfilling specs for behaviour no change has touched.
- Moving, converting or retiring ADRs, architecture pages or the contract registry.
- Replacing Corvint Tasks, its gates or its completion rules.
- Changing `make gate` in this change (see tasks: validation is wired in a follow-up task).

## Capabilities

### New Capabilities

- `change-workflow`: how a change is proposed, linked to a ticket, validated and archived.

### Modified Capabilities

None.

## Impact

- Files: `openspec/**`, `AGENTS.md`, `.gitignore`. No product code.
- Failure modes: a proposal leaks private material into this public repository; a proposal goes
  stale after landing because it was never archived; `tasks.md` is used as a second queue.
  The requirements in `specs/change-workflow/spec.md` address each.
- Acceptance evidence: `openspec validate --all --strict` passes; one real feature change is taken
  from proposal to archive under this workflow; Corvint indexes the change documents.
- Rollback: delete `openspec/`, the `AGENTS.md` section and the `.gitignore` line. Nothing else
  depends on them.
