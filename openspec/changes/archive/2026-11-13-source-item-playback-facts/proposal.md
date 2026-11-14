# Source-item playback facts: prove the existing `engagement` field

Ticket: CORE-0353 (`queue:core:main`).

## Why

The TV hero's resume label ("Resume · 42:18 left", "Play again") and the Continue Watching progress
bar need the acting Profile's position, duration and finished flag on each `SourceItem`. When the
ticket was written the wire `SourceItem` carried none of these.

Since then CORE-1192 (e8afb3f3) shipped `SourceItem.engagement` on every `SourceItem` read, and it
is in the published wire contract 2.0.0:

- `internal/graph/source_engagement.go`: `positionSeconds` and `durationSeconds` while resumable,
  `completed` once finished, and `totalCount` / `remainingCount` on ordered containers.
- `internal/store/engagement_annotate.go` (`AnnotateEngagement`, `progressByMediaLocked`): one
  `profile_id = ? AND media_id IN (...)` query per 500 IDs. An empty Profile is a no-op, and
  items without state stay absent.

Adding the ticket's `playback` field beside `engagement` would put the same facts on the wire
twice. This change therefore adds no wire field. It records the two acceptance cases the existing
spec did not state, cross-Profile isolation and the batched read, and proves both with tests.

## What Changes

- The `web-card-watch-state` requirement gains two scenarios: another Profile's progress, and one
  query per page.
- New store tests: `TestProgressByMediaBatchesPerProfile` (counting driver) and
  `TestAnnotateEngagementHidesAnotherProfilesProgress`.
- No wire, schema or golden change.

## Mapping to the ticket

| Ticket acceptance | Delivered by |
|---|---|
| `playback.positionSeconds` / `durationSeconds` | `engagement.positionSeconds` / `durationSeconds`, present while resumable |
| `playback.watched` | `engagement.completed` |
| Absent without a progress row | `engagement` absent |
| Absent on Entity items | Entity items carry container counts only, never a position |
| One batched query per page | `progressByMediaLocked`: one query per 500 IDs |
| Another Profile never appears | the query is keyed by the acting `profile_id` |

## Ordinal gap (not shipped)

The ticket also asks for `ordinal` (`part`, `container`), present only when a number came from a
plugin-declared ordinal field. Without a schema change, the stored relation row cannot tell that
source apart from the scrape array-index fallback:

- `internal/identify/identify.go:559-573` (`relationSortOrder`) returns either the plugin
  `OrdinalField` value or the scrape `SortOrder` (the array index). Both land in the same
  `entity_relation.sort_order` column.
- `internal/store/migrate/catalog/0019_relation_sort_order.sql` adds only `sort_order INTEGER`.
  There is no provenance column.
- `entity_relation.attributes` is written by plugins and exported on the wire, so it cannot carry
  provenance the server trusts.

As the ticket directs, `ordinal` is omitted. CORE-1416 tracks the follow-up: a new migration that
records ordinal provenance.

## Non-goals

- A second wire field duplicating `engagement`.
- The `ordinal` field (see above).

## Rollback

Revert the change. It touches only tests and spec text.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `web-card-watch-state`: cross-Profile isolation and batched-read scenarios.
