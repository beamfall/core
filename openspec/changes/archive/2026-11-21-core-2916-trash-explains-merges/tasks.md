# Tasks

- [x] Store derives the system actor `scan` for a `media.merge` entry with no session, Profile,
      token or key.
- [x] `activityLog` / `activityEntry` expose `systemActor`; the web activity sentence names the scan.
- [x] Store read of merge-trashed Media (`op_batch` kind `merge-version`, not undone) with survivor
      title; `trashList` returns `mergedInto`.
- [x] Store `DuplicateCopies` and manager query `duplicateCopies`.
- [x] Manage › Trash: merged batches with Undo merge, deletions apart, duplicate copies section.
- [x] Tests: store (merge info, purge keeps survivor files, duplicates, actor), graph, web.
- [x] Biome, tsc, strings tests; `openspec validate --all --strict`.
