# Tasks (CORE-1676)

- [x] 1. Store: `ImportMediaFileVersion`, which also moves a labelled anchor out of the default slot; Go tests
- [x] 2. Scan: group same-title files of one directory into Versions, seeding titles an earlier scan cataloged; a Version copied into another library detaches; Go tests
- [x] 3. playbackInfo: `selectedVersion` and `versions`; Stream Token `fileFingerprint`; byte routes; Go tests
- [x] 4. Wire goldens regenerated (the versioned artifact is published once for the landing stack)
- [x] 5. Web player version menu with a remembered choice; tests
- [x] 6. Repin plugin-sdk v0.3.5 and plugin-filmtv with the version label (pseudo-versions of the PR heads; repin to the v0.3.5 and v0.1.8 tags when they are cut)
- [x] 7. Each Version judged by its own container and codec; an alternate of unknown height is transcoded under a height cap; the Media row follows the first Version; Go tests
- [ ] 8. Native pickers in the client repositories (separate PRs)
