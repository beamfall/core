# Tasks (CORE-1144)

- [x] 1. `media(id)` client query and identity-bound nav state carrying the opening card
      (`src/areas/viewer/lib/mediaDetail.ts`); cached sibling lookup; tests
- [x] 2. `MediaDetailPage`: header (artwork, title, year, summary, declared fields), Play/Resume
      with progress, back, skeleton, not-available and error states, "more from" shelf, phone
      layout; strings in en, fr, de, es and pt; tests
- [x] 3. Card activation: poster cards in shelf and grid sections open the page; list rows and the
      hero keep playing at once; tests
- [x] 4. Register `/media/:mediaId` in the viewer area routes
- [ ] 5. Owner review in the running UI; `make gate` at landing; `openspec archive media-detail-page`
