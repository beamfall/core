## 1. Server

- [x] 1.1 Catalog migration 0116: `media_metadata_provenance.candidate_id`, `media_sort_title`
- [x] 1.2 Store: rank-gated `UpdateMediaMetadata` with sort title, lockable sort title and artwork slots, held plugin field locks
- [x] 1.3 Identify: remember the candidate; skip locked artwork slots
- [x] 1.4 Graph: `mediaMetadata`, `editMediaMetadata`, `refreshMediaMetadata`; activity kinds
- [x] 1.5 Library browse and letter rail sort by sort title
- [x] 1.6 Go tests: edit locks, refresh keeps locked fields, unlock lets refresh win, locked artwork slot
- [x] 1.7 Wire baselines and graph goldens regenerated; backup fixture corpus regenerated

## 2. Web

- [x] 2.1 Client calls and types
- [x] 2.2 Manage "Edit item" page with lock toggles, plugin fields, artwork and refresh; collection member link
- [x] 2.3 vitest: edit, lock, plugin field, artwork lock, refresh, refresh disabled

## 3. Follow-ups (not in this change)

- [ ] 3.1 Viewer media detail More/Manage link to `/manage/items/:mediaID`
- [ ] 3.2 Publish wire contract 2.8.0 (batch landing)
- [ ] 3.3 Genres stored as related entities

## 4. Gate and archive

- [ ] 4.1 `make gate`
- [ ] 4.2 Archive the change
