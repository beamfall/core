# Persist the Entity merge winner so Undo touches only the merged pair

Ticket: CORE-0743 (`queue:core:main`).

## Why

`MergeEntities` journals the loser's and winner's prior relation rows but not the winner's id.
Undo then guesses the winner from the first journaled loser row. When the loser points at an
unrelated Entity T (for example `loser -friend-> T`), Undo picks T as the "winner", deletes every
relation row touching T (including T's unrelated media relations) and never clears the real
winner's post-merge rows. One Undo silently destroys third-party state.

## What Changes

- The merge journal item (`op_batch_entity_item.prior_state`, JSON) carries the winner's Entity id.
  No schema change: the field rides the existing JSON column.
- Undo reverses the merge only for the journaled loser and winner. It no longer infers the winner.
- Undo of a journal item without a winner id (written before this change) is refused with a
  validation error, the transaction rolls back, and nothing changes. The batch stays not undone.

## Non-goals

- Preserving relations added to the winner after the merge; Undo still restores the winner's
  pre-merge snapshot, as before.
- Migrating old journal rows. Their winner cannot be recovered without guessing.

## Failure modes

- An entity merge made before this change can no longer be undone through `UndoBatch`; the caller
  gets a validation error. The loser stays in Trash and can still be restored with `RestoreEntity`.

## Acceptance evidence

- `TestAuditUndoEntityMergePreservesThirdParty` in `internal/store/entity_merge_test.go`: merge L
  into W while L points at T and T holds unrelated media relations; Undo restores every relation
  row exactly and leaves T's relations unchanged.
- `TestUndoEntityMergeRefusesJournalWithoutWinner`: a journal item with no winner id is refused
  and mutates nothing.

## Rollback

Revert the change. Journal items written with the winner field still undo under the old code,
which ignores the unknown JSON field and infers the winner as before.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `store-integrity`: adds the Entity merge undo requirement.
