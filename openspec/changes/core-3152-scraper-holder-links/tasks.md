## 1. Server

- [x] 1.1 Add `store.RemoveMediaRelations` deleting a Media's rows under the named relations
- [x] 1.2 `applyEntities` removes a Media's stale single-holder link when the applying scraper's map hydrates nothing
- [x] 1.3 A single-holder link without a map ordinal takes the identified year as its sort order

## 2. Tests

- [x] 2.1 Store test: removal deletes only the named relations of that Media
- [x] 2.2 App test: two lookups sharing a container object hydrate one container Entity, listed in year order through the entity-media read path and the container chain; a match with no container removes the link and a match with another container moves it
- [x] 2.3 Existing identify tests stay green: a spec without such a map issues no removal, and a lookup without entities writes nothing
