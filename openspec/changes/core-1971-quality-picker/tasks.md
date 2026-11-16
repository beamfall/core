# Tasks (CORE-1971)

- [x] 1. Server: negotiation tests for ladder steps (step tops out at itself, admin cap wins when
      tighter, no ceiling unchanged); no server or wire change
- [x] 2. Web: per-browser quality choice store; `maxStreamingBitrate` on playback-info and pre-play
      requests
- [x] 3. Web: quality menu marks the remembered step and offers Auto on direct play while one is
      remembered; a pick or a reset re-requests playback info at the playhead
- [x] 4. Web unit tests (choice store, request body, menu model, selection)
- [ ] 5. Follow-up: Apple player menu in the Apple client repository
- [ ] 6. `make gate`; archive this change after landing
