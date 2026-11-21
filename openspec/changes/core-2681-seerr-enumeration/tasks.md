# Tasks (CORE-2681)

- [x] Repin plugin-sdk v0.4.6 and the bundled plugins, and fix the fixtures the repin moved.
- [x] Add a store read of one Library's visible Media under the caller's filter, catalog plane only.
- [x] Add a batched metadata provenance read.
- [x] Flatten each bundle's Interop, container relations and scraper ID schemes into compat config.
- [x] Serve `Users`, `Users/{id}`, `Users/{id}/Images/Primary` and a real `Library/MediaFolders`.
- [x] Serve `Items?ParentId=`, `Items?ids=`, `Items/Latest`, `Shows/{id}/Seasons` and `Shows/{id}/Episodes`.
- [x] Queue a full eligible refresh for an all-relative media update.
- [x] Tier-1 tests: allowlisted, non-allowlisted, Reveal-restricted and certification-restricted, by item and by count.
- [x] Update the route inventory test.
- [x] Record the enumeration and the relative-path rule in ADR-0171 §2.8.
- [ ] Recorded Seerr run: users import and availability for an allowlisted Library.
