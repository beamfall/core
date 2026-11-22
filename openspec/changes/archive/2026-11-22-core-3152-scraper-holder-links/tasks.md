## 1. Server

- [x] 1.1 Add the nullable `entity_relation.writer` column (catalog 0131, encrypted library 0012) and `store.RemoveWrittenMediaRelations` deleting only a writer's rows from Entities of the named kind
- [x] 1.2 `applyEntities` records the scraper as writer and removes its stale single-holder link when the map ran and hydrated nothing; `scrape.Result.SkippedEntityMaps` marks skipped maps
- [x] 1.3 A single-holder link without a map ordinal takes the stored year after field precedence as its sort order
- [x] 1.4 A skipped map protects its kind and relation from removal; a holder link keeps the stored title through `store.UpsertEntityKeepTitle`, trashed or not; the container map applies only to a holder this lookup linked; the merge journal records and undo restores each row's writer

## 2. Tests

- [x] 2.1 Store test: removal deletes only the writer's rows of the named kind and relation on that Media; an operator re-link takes a row over
- [x] 2.2 App test: two lookups sharing a container object hydrate one container Entity, listed in year order through the entity-media read path and the container chain; the holder is keyed by provider id (rename keeps one Entity, a same-name container stays apart); a manual year orders; a match with no container removes the scraper's link, keeps an operator's, and a match with another container moves it
- [x] 2.4 App test: a holder map skipped for an unknown ordinal keeps the link, even beside an empty sibling map; both resolving nothing removes it
- [x] 2.5 App test: a container object with an id but no name keeps the stored title; one with a name but no id leaves an operator-linked holder's title alone
- [x] 2.7 App test: an id-only lookup keeps a trashed holder's title across restore and an operator rename landing before the write; a new holder without a name takes the id
- [x] 2.6 Store test: Entity merge undo restores each relation row's writer
- [x] 2.3 Existing identify tests stay green: a spec without such a map issues no removal, and a lookup without entities writes nothing
