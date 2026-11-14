## 1. Related Entities

- [x] 1.1 Store read: related Entities in both directions, grouped by kind, visibility-filtered
- [x] 1.2 Graph: `relatedEntities` groups on `entity(id)` with the kind's display name
- [x] 1.3 Web: Entity page renders related groups before the Media list or detail Layout

## 2. Tests

- [x] 2.1 Failing tests first: store directions, visibility, ordering; graph groups; web rendering
- [x] 2.2 Nested schema and wire-contract goldens regenerated through their documented commands

## 3. Verify and close

- [x] 3.1 Focused package tests, `internal/conformance`, web lint, typecheck and tests
- [ ] 3.2 Run `make gate`
- [ ] 3.3 Run `openspec archive entity-related-entities` after landing, before completing CORE-1210
