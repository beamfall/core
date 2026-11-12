## 1. Labels and times

- [ ] 1.1 Map every server activity action to a sentence and a category, with a readable fallback
- [ ] 1.2 Format relative, local and exact UTC times in the viewer's locale and time zone
- [ ] 1.3 Fail a test when a server action has no sentence

## 2. Activity page

- [ ] 2.1 Read the acting identifiers and resolve Profile, device and Library names
- [ ] 2.2 Render sentences, times and per-row details
- [ ] 2.3 Filter by category and search by text

## 3. Verify and close

- [ ] 3.1 Check in the running Console on the demo seed
- [ ] 3.2 Run `make gate`
- [ ] 3.3 Run `openspec archive console-activity-log` after landing, before completing CORE-1198
