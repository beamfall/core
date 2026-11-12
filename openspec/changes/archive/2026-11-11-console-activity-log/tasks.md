## 1. Labels and times

- [x] 1.1 Map every server activity action to a sentence and a category, with a readable fallback
- [x] 1.2 Format relative, local and exact UTC times in the viewer's locale and time zone
- [x] 1.3 Fail a test when a server action has no sentence

## 2. Activity page

- [x] 2.1 Read the acting identifiers and resolve Profile, device and Library names
- [x] 2.2 Render sentences, times and per-row details
- [x] 2.3 Filter by category and search by text

## 3. Verify and close

- [x] 3.1 Check in the running Console on the demo seed
- [x] 3.2 Run `make gate`
- [x] 3.3 Run `openspec archive console-activity-log` after landing, before completing CORE-1198
