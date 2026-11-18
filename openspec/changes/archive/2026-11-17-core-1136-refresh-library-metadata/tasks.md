## 1. Server

- [x] 1.1 Store: keyset list of live Media ids in one Library
- [x] 1.2 identify: `Refresh(mediaID)` shared by the single-item mutation and the job; skip sentinels
- [x] 1.3 identify: `library-metadata-refresh` job (dedupe, pacing, checkpoint, progress, cancel)
- [x] 1.4 Register the job handler in the composition root
- [x] 1.5 GraphQL `refreshLibraryMetadata(libraryID)` with `scanLibrary` authority and token tiers
- [x] 1.6 Go tests: job creation, dedupe, cancel, locks kept, skips, progress, authority

## 2. Web Console

- [x] 2.1 Client call `refreshLibraryMetadata`
- [x] 2.2 Library card "Refresh all metadata" with confirmation; strings in every locale
- [x] 2.3 Operations label, description and progress message for the job kind
- [x] 2.4 vitest for the action

## 3. Gate and archive

- [ ] 3.1 `make gate`
- [ ] 3.2 Archive the change
