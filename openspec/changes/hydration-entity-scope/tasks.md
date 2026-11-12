## 1. Scoped identity

- [ ] 1.1 `HydrationEntity.Scope` and the scoped id formula
- [ ] 1.2 `EntityScopes` declaration and plan validation
- [ ] 1.3 Music mapper scopes albums by album artist, falling back to artist

## 2. Existing databases

- [ ] 2.1 Adopt a compatible title-only Entity instead of creating a scoped twin
- [ ] 2.2 One-time repair reopening title-only Entities that record no scope or several

## 3. Tests

- [ ] 3.1 Failing test first: two same-titled albums by different album artists merge
- [ ] 3.2 Adoption, refusal to adopt another scope's Entity, compilation stays one album
- [ ] 3.3 Store: relation targets read and the one-time split

## 4. Verify and close

- [ ] 4.1 Focused package tests and `internal/conformance`
- [ ] 4.2 Run `make gate`
- [ ] 4.3 Run `openspec archive hydration-entity-scope` after landing, before completing CORE-1212
