## 1. Inheritance

- [x] 1.1 Inherited-poster copy with a source record, atomic and idempotent
- [x] 1.2 Store reads: Entities of a kind by id cursor, member Media in order, Entities pointing at an Entity
- [x] 1.3 Entity pass in the artwork sweep with its own checkpoint
- [x] 1.4 Provider import clears the inherited mark; identification treats inherited as absent
- [x] 1.5 Hook declaration and the music wiring

## 2. Tests

- [x] 2.1 Failing tests first for member-Media and related-Entity inheritance
- [x] 2.2 Provider wins, idempotence, refresh on source change, undeclared kinds

## 3. Verify and close

- [x] 3.1 Focused package tests and `internal/conformance`
- [ ] 3.2 Run `make gate`
- [ ] 3.3 Run `openspec archive entity-poster-inheritance` after landing, before completing CORE-1211
