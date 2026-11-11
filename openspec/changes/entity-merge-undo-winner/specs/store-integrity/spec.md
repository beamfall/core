## ADDED Requirements

### Requirement: Entity merge undo reverses only the merged pair

`MergeEntities` SHALL journal the winner's Entity id with the loser's item. Undo of an entity merge
SHALL restore the journaled relation rows of that loser and that winner and un-trash the loser. It
SHALL NOT infer the winner from relation rows, and SHALL NOT alter relation rows that touch
neither the loser nor the winner.

#### Scenario: Loser points at an unrelated Entity

- **WHEN** Entity L relates to Entity T, T holds relations to Media that involve neither L nor the
  winner W, L is merged into W, and the merge batch is undone
- **THEN** every relation row is as it was before the merge, T's relations are unchanged, and L is
  live again

### Requirement: Entity merge undo refuses journals without a winner

Undo of an entity merge journal item that has no winner id SHALL fail with a validation error and
SHALL change nothing, leaving the batch not undone.

#### Scenario: Journal written without the winner id

- **WHEN** an entity merge batch whose journal item has no winner id is undone
- **THEN** the undo fails with a validation error, no relation row changes, the loser stays
  trashed, and the batch is not marked undone
