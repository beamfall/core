## 1. Server

- [x] 1.1 `sourceItems` kind `collection`: curated order, paging, visibility and Mute filters
- [x] 1.2 Go tests for order and visibility

## 2. Web

- [x] 2.1 `/collections` page grouped by library, manager create form
- [x] 2.2 `/collections/:libraryId/:collectionId` page: ordered entries, Play all queue
- [x] 2.3 Manager editing: move up/down (hidden members keep place), remove, add from library search
- [x] 2.4 Account menu link; strings in `strings/viewer-collections.ts`
- [x] 2.5 vitest for the move rule, both pages and failure states

## 3. Follow-ups (not in this change)

- [ ] 3.1 Native clients: iPhone, tvOS, macOS, Android playlists and collection pages
- [ ] 3.2 Plugin-provided box-set source feeding Collections
- [ ] 3.3 Add the new routes to the e2e UI-state route list

## 4. Gate and archive

- [ ] 4.1 `make gate`
- [ ] 4.2 Archive the change
