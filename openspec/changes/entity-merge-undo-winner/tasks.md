## 1. Journal and undo

- [ ] 1.1 Journal the winner's Entity id in the merge item state
- [ ] 1.2 Undo reverses only the journaled loser and winner, with no inference
- [ ] 1.3 Refuse undo of a journal item without a winner id, mutating nothing

## 2. Tests

- [ ] 2.1 `TestAuditUndoEntityMergePreservesThirdParty`
- [ ] 2.2 `TestUndoEntityMergeRefusesJournalWithoutWinner`

## 3. Verify and close

- [ ] 3.1 Run `make gate`
- [ ] 3.2 Run `openspec archive entity-merge-undo-winner` after landing, before completing CORE-0743
