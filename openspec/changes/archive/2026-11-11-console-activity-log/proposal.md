# Console Activity log reads as sentences with local times

Ticket: CORE-1198 (`queue:core:main`).

## Why

Console > Monitor > Activity lists raw event keys (`media.trash`) and ISO-8601 UTC timestamps. An
owner has to know the server's action vocabulary and convert time zones to read their own audit
trail.

## What Changes

- Each row reads as a plain-language sentence naming who acted, what they did and what it was
  done to. Profile, device and Library names are resolved from the lists the Console already
  loads; the row still renders when a name cannot be resolved.
- An action the web app has no sentence for falls back to a readable form of its key.
- The raw action key, target, identifiers and exact UTC time are available per row on demand.
- Times show as relative time plus the local date and time in the viewer's locale and time zone,
  with the exact UTC time on hover.
- The log can be filtered by event category and searched by text. The page reads the latest 200
  entries instead of 50.
- A test reads the server's closed action vocabulary (`internal/store/activity.go`) and fails
  when an action has no sentence.

## Non-goals

- Changing the activity log's stored shape, the server vocabulary or the reveal filter.
- Resolving Media titles. An entry names only an opaque Media id, and the title of a trashed or
  purged item may be gone; rows say "an item" and keep the id in the details.
- Translating the sentences. They live in one English table beside the mapping until the
  localized string registry is free to take them (another lane holds it at the time of writing).
- Server-side filtering or paging beyond the latest 200 entries.

## Failure modes

- A name lookup (Profiles, devices, Libraries) fails or a name is gone: the row falls back to a
  generic noun and the identifiers stay in the details.
- A new server action ships without a sentence: the coverage test fails at the gate; at runtime
  the row shows the readable key.

## Acceptance evidence

- `src/lib/activityDisplay.test.ts`: every server action has a sentence and a category; unknown
  keys humanize; relative, local and UTC time formatting.
- `src/areas/console/sections/ActivityLog.test.tsx`: sentences, details on demand, category
  filter and search.
- Checked in the running Console on the demo seed.

## Rollback

Revert the change. No stored state depends on it.

## Capabilities

### New Capabilities

- `console-activity-log`: how the Console presents the activity log to an operator.

### Modified Capabilities

None.
