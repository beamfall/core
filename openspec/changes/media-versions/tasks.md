# Tasks (CORE-1676)

- [x] 1. Store: `ImportMediaFileVersion`, which also moves a labelled anchor out of the default slot; Go tests
- [x] 2. Scan: group same-title files of one directory into Versions, seeding titles an earlier scan cataloged; a Version copied into another library detaches; Go tests
- [x] 3. playbackInfo: `selectedVersion` and `versions`; Stream Token `fileFingerprint`; byte routes; Go tests
- [x] 4. Wire goldens regenerated (the versioned artifact is published once for the landing stack)
- [x] 5. Web player version menu with a remembered choice; tests
- [x] 6. Repin plugin-sdk v0.3.5 and plugin-filmtv v0.1.8 with the version label, and every other bundled plugin on a release that declares SDK range [0.3.5, 0.3.5] (audiobook, familyarchive, photos and podcasts v0.1.6, karaoke v0.1.2, music v0.1.7)
- [x] 7. Each Version judged by its own container and codec; an alternate of unknown height is transcoded under a height cap; the Media row follows the first Version; Go tests
- [x] 7a. The row keeps the first Version's facts through a re-import of another Version, a missing first file and an unprobed first Version; every Version of unknown height in a multi-Version Media is guarded; Go tests
- [ ] 8. Native pickers in the client repositories (separate PRs)
