## 1. Server

- [x] 1.1 Store: validate the reserved `profile.avatar` preference; read it for every Profile
- [x] 1.2 Graph: `ProfileSummary.avatar`; regenerate the wire goldens
- [x] 1.3 Tests: round trip, clear, refusals, picker payload

## 2. Web

- [x] 2.1 `ProfileAvatar` (generated and chosen) and the avatar set
- [x] 2.2 Picker: avatar tiles and the in-place PIN prompt
- [x] 2.3 Picker on entry; header avatar link
- [x] 2.4 Avatar chooser in profile settings
- [x] 2.5 Signed-out page: where a pairing code comes from, other sign-in routes
- [x] 2.6 Strings and tests

## 3. Verify and close

- [x] 3.1 Browser check on the demo seed at phone and desktop widths
- [x] 3.2 `openspec validate --all --strict`
- [x] 3.3 `make gate`
