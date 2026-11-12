## 1. Server

- [ ] 1.1 Store: validate the reserved `profile.avatar` preference; read it for every Profile
- [ ] 1.2 Graph: `ProfileSummary.avatar`; regenerate the wire goldens
- [ ] 1.3 Tests: round trip, clear, refusals, picker payload

## 2. Web

- [ ] 2.1 `ProfileAvatar` (generated and chosen) and the avatar set
- [ ] 2.2 Picker: avatar tiles and the in-place PIN prompt
- [ ] 2.3 Picker on entry; header avatar link
- [ ] 2.4 Avatar chooser in profile settings
- [ ] 2.5 Signed-out page: where a pairing code comes from, other sign-in routes
- [ ] 2.6 Strings and tests

## 3. Verify and close

- [ ] 3.1 Browser check on the demo seed at phone and desktop widths
- [ ] 3.2 `openspec validate --all --strict`
- [ ] 3.3 `make gate`
