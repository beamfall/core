## ADDED Requirements

### Requirement: Entity merge undo reverses only the merged pair

`MergeEntities` SHALL journal the winner's Entity id with the loser's item. Undo of an entity merge
SHALL restore the journaled relation rows of that loser and that winner, SHALL un-trash the loser,
and SHALL NOT delete or alter relation rows of any other Entity except rows that touch the loser or
winner. Undo SHALL NOT infer the winner from relation rows.

Undo of an entity merge journal item that has no winner id SHALL fail with a validation error and
SHALL change nothing, leaving the batch not undone.

#### Scenario: Loser points at an unrelated Entity

- **WHEN** Entity L relates to Entity T, T holds relations to Media that involve neither L nor the
  winner W, L is merged into W, and the merge batch is undone
- **THEN** every relation row is as it was before the merge, T's relations are unchanged, and L is
  live again

#### Scenario: Journal written without the winner id

- **WHEN** an entity merge batch whose journal item has no winner id is undone
- **THEN** the undo fails with a validation error, no relation row changes, the loser stays
  trashed, and the batch is not marked undone
