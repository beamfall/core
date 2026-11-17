## 1. Server

- [ ] 1.1 Store: keyset list of live Media ids in one Library
- [ ] 1.2 identify: `Refresh(mediaID)` shared by the single-item mutation and the job; skip sentinels
- [ ] 1.3 identify: `library-metadata-refresh` job (dedupe, pacing, checkpoint, progress, cancel)
- [ ] 1.4 Register the job handler in the composition root
- [ ] 1.5 GraphQL `refreshLibraryMetadata(libraryID)` with `scanLibrary` authority and token tiers
- [ ] 1.6 Go tests: job creation, dedupe, cancel, locks kept, skips, progress, authority

## 2. Web Console

- [ ] 2.1 Client call `refreshLibraryMetadata`
- [ ] 2.2 Library card "Refresh all metadata" with confirmation; strings in every locale
- [ ] 2.3 Operations label, description and progress message for the job kind
- [ ] 2.4 vitest for the action

## 3. Gate and archive

- [ ] 3.1 `make gate`
- [ ] 3.2 Archive the change
