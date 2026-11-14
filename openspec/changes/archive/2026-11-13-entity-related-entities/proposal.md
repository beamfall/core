# List an Entity's related Entities on the Entity view

Ticket: CORE-1210 (`queue:core:main`).

## Why

The Entity view reads only Entity-to-Media relations (`mediaForEntitySQL` behind `mediaForEntity`
and the `entity-media` Source). Nothing reads Entity-to-Entity relations for a viewer, so a page
for an Entity lists its Media but never the Entities linked to it. In a music Library an artist
page lists tracks but no albums, and an artist with no directly linked tracks (the compilation
artist an album names only through its album-artist relation) opens to an empty page.

## What Changes

- Store: a reveal-filtered read of the Entities related to one Entity through Entity-to-Entity
  relations in both directions: the Entities it points at and the Entities that point at it.
  Trashed Entities, the Entity itself and Entities in Libraries the filter hides are omitted, and
  the context Entity must itself be visible (an Entity hidden behind the Reveal contract errors
  like a missing one, exactly as the Media read does). An Entity reached by several relation rows
  appears once. The result is grouped by Entity kind (kinds in name order); within a group rows
  carrying a relation sort order come first in that order, then by title and id.
- Graph: `entity(id)` gains `relatedEntities`, a list of groups. Each group carries the kind, the
  owning plugin's display name for it, and its Entities as Source items (kind `entity`, with the
  stored artwork the existing Entity cards already render). Other reads that share the Entity
  wire shape leave it empty.
- Web: the viewer Entity page renders each related group under the kind's display name (falling
  back to the kind) as a grid of the existing item cards, before the Media list or detail Layout.
  The groups render whether or not the Entity has direct Media, so an Entity with relations never
  opens empty.

## Non-goals

- The SDK half of the ticket's first acceptance criterion: a plugin-declarable Entity-to-Entity
  `SourceSpec` kind. Adding it changes `plugin-sdk` and forces every bundled plugin onto a new
  release; it is a follow-up ticket. This change is the Core read that kind will dispatch to.
- Paging related groups. They are bounded by the catalog's relations for one Entity, as the
  Media list already is.
- Filtering by relation name or choosing which kinds a plugin wants shown; every
  Entity-to-Entity relation is listed.
- Native clients (Apple, Android, Windows); they can select the new field when they adopt it.

## Failure modes

- A related Entity in a hidden or trashed Library is omitted, never shown as a placeholder, so the
  read leaks nothing past the Reveal contract.
- A kind whose declaration cannot be resolved (plugin-less Library, unloaded bundle) is grouped
  under its schema kind name with no display name; the client shows the kind name.
- A store error on the related read fails the `entity(id)` read, like any other store error.

## Acceptance evidence

- `internal/store/entity_related_test.go`: `TestRelatedEntityGroupsBothDirections`,
  `TestRelatedEntityGroupsHonorVisibility`, `TestRelatedEntityGroupsHiddenContextEntity`,
  `TestRelatedEntityGroupsOrderBySortOrderThenTitle`.
- `internal/graph/entity_related_test.go`: `TestEntityQueryListsRelatedEntityGroups`.
- `internal/web/app/src/areas/viewer/pages/EntityPage.test.tsx`: related groups render before the
  Media list and on an Entity with no direct Media.

## Rollback

Revert the change. The read and the field are additive; no schema migration, stored data or
configuration is involved.

## Capabilities

### New Capabilities

- `entity-related-entities`: which related Entities an Entity view lists, in what order and under
  which visibility rules.

### Modified Capabilities

None.
