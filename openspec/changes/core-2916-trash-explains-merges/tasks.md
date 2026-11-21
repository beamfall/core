# Tasks

- [ ] Store derives the system actor `scan` for a `media.merge` entry with no session, Profile,
      token or key.
- [ ] `activityLog` / `activityEntry` expose `systemActor`; the web activity sentence names the scan.
- [ ] Store read of merge-trashed Media (`op_batch` kind `merge-version`, not undone) with survivor
      title; `trashList` returns `mergedInto`.
- [ ] Store `DuplicateCopies` and manager query `duplicateCopies`.
- [ ] Manage › Trash: merged batches with Undo merge, deletions apart, duplicate copies section.
- [ ] Tests: store (merge info, purge keeps survivor files, duplicates, actor), graph, web.
- [ ] Biome, tsc, strings tests; `openspec validate --all --strict`.
