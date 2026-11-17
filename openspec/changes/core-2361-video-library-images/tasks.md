- [x] `internal/scan`: `ImageRule` from the plugin's declared classes drives the walk, the missing
      pass's skip, the preview and the estimate
- [x] `internal/scan`: a rescan retires image Media the rule no longer imports, before the missing
      pass, only under the root and only on a non-empty walk
- [x] `internal/store`: `TrashMediaRetiredByScan` soft-deletes through the `trashMedia` path, audits
      `media.trash`, and refuses while trash is disabled
- [x] `internal/graph`: preview and estimate take the bound plugin's image rule
- [x] Tests: stray image skipped in a video Library, kept with an image plugin and with no plugin;
      catalogued sidecar and stray image retired to trash with audit; empty walk retires nothing;
      trash disabled refuses
- [ ] `make gate`
- [ ] After deploy, library-tv reports 0 image-class Media
- [ ] `openspec archive core-2361-video-library-images` after landing
