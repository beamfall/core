# Tasks

- [ ] Events migration adding `activity_entry.system_actor`; store writes `scan` on the
      verified-copy and split-version `media.merge` entries.
- [ ] `activityLog` / `activityEntry` expose `systemActor`; the web activity sentence names the scan.
- [ ] Store read of merge-trashed Media (`op_batch` kind `merge-version`, not undone) with survivor
      title; `trashList` returns `mergedInto`.
- [ ] Store `DuplicateCopies` and manager query `duplicateCopies`.
- [ ] Manage › Trash: merged batches with Undo merge, deletions apart, duplicate copies section.
- [ ] Tests: store (merge info, purge keeps survivor files, duplicates, actor), graph, web.
- [ ] Biome, tsc, strings tests; `openspec validate --all --strict`.
