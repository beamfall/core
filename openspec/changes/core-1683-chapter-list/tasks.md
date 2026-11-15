## 1. Web player (beamfall)

- [x] 1.1 `buildChapterList` in `areas/viewer/lib/transport.ts`: chapter rule, thumbnail URL, current chapter
- [x] 1.2 `PlaybackAssets` carries `markerThumbnailBase`; a 404 or expired-token thumbnail falls back to the trickplay tile
- [x] 1.3 `ChapterList` control in the transport row: list, seek on select, Escape closes, trickplay fallback
- [x] 1.4 Unit and component tests; the chapters menu is inventoried in `e2e/lib/ui-states.mjs` as an uncovered gap

## 2. Other clients

- [ ] 2.1 Android TV chapter list (`beamfall-android` app-tv)
- [ ] 2.2 iPhone and macOS chapter list (`beamfall-apple`)
