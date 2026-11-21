## 1. Server

- [x] 1.1 `RelatedEntityGroups` derives each related Entity's direction from the relation rows and groups by kind and direction
- [x] 1.2 `RelatedEntityGroupInfo` publishes `direction`; wire goldens regenerated with `make regen-wire-goldens`
- [ ] 1.3 Publish the client-core-wire minor version and repin consumers (lander)

## 2. Web

- [x] 2.1 The Entity query reads `direction`; `nextUpChildContainer` reads only child groups

## 3. Tests

- [x] 3.1 Store test over a three-level container graph, a kind related both ways and an Entity linked both ways
- [x] 3.2 Graph test asserts each group's direction
- [x] 3.3 Vitest: `nextUpChildContainer` and the Entity page skip a parent container group sorting first
