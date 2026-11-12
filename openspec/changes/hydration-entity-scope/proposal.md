# Scope hydrated Entity identity so same-titled albums stay separate

Ticket: CORE-1212 (`queue:core:main`).

## Why

Local-metadata hydration derives an Entity id from `(library, kind, title)`. The music mapper keys
an album by its title alone, so two different albums called "Greatest Hits" in one Library become
one Entity: one track list, one release date, both album artists. `TestHydrationSeparatesSameTitledEntitiesByScope`
reproduces it before the fix.

## What Changes

- `app.HydrationEntity` gains an optional `Scope`. A scoped Entity's id derives from
  `(library, kind, scope, title)`; `Title` stays the display title. An empty scope keeps the
  existing title-only id, so artists, genres and every other mapper are unchanged.
- A mapper that scopes a kind declares the entity→entity relation that records the scope
  (`EntityScopes`). Core stays domain-agnostic: it sees a kind, a relation name and a string.
- The music mapper scopes an album by its album artist (the album-artist tag, falling back to the
  path-derived artist the plugin already resolves, then to the first credited artist) and always
  writes the album-artist relation for that scope.
- Existing databases, no migration:
  - A title-only Entity written before this change is adopted (keeps its id, artwork and
    references) when its scope relation points at nothing or only at the scope being hydrated. A
    correctly keyed album is neither orphaned nor duplicated on rescan.
  - A title-only Entity whose scope relation points at two or more Entities is a merge made by the
    old key, and one that records no scope at all (tracks with no album-artist tag) may be one.
    Once, at startup, Core reopens both: it clears their scope relation rows and the hydration
    marker of their member Media. The next scan re-reads those files' tags: the first scope seen
    adopts the old id, every other scope gets its own Entity, and each track moves with its album.
    An Entity that records exactly one scope is left alone. The repair is limited to Libraries of
    the plugin whose mapper declares the scope.

## Non-goals

- Case or punctuation folding of album artists ("Beatles" and "The Beatles" stay distinct, as
  artist Entities already do).
- Encrypted Libraries: the one-time repair covers the shared catalog plane only. Their Entities are
  adopted or scoped as their files are re-hydrated.
- Provider (MusicBrainz) album Entities. None exist today; artists stay keyed by name.

## Failure modes

- The first scan after upgrade re-reads the tags of every track on an album that recorded no album
  artist or more than one. In a library with no album-artist tags that is every track, once.
- Files of a formerly merged album that are offline at the next scan keep their old membership and
  lose only the hydration marker; they are re-read when they return.
- A second scope relation added by hand to a scoped Entity is not rewritten after the one-time
  split has run (the split is recorded in `server_config` and never repeats).
- Tracks of one album whose album artist is untagged, not derivable from the path and differs per
  track split into one album per artist. This is the stated key (album artist, falling back to
  artist); tagging an album artist or the compilation flag keeps them together.

## Acceptance evidence

- `TestHydrationSeparatesSameTitledEntitiesByScope`, `TestHydrationAdoptsUnscopedEntityForItsOnlyScope`,
  `TestHydrationKeepsUnscopedEntityOfAnotherScope` and, on a real Store through the repair,
  `TestHydrationSplitsMergedEntityAfterScopeRepair` in `internal/app/musichydration_test.go`.
- `TestSplitAmbiguousEntityScopesRequeuesMembersOnce` and `TestEntityRelationTargets` in
  `internal/store/entity_scope_test.go`.
- `TestMusicAlbumIdentityIsAlbumArtistAndTitle` in `cmd/beamfall/hooks_test.go`: same title with
  different album artists yields two scopes; a compilation with one album artist yields one.

## Rollback

Revert the change. Entities created under the scoped key keep working under the old code as
ordinary Entities; a later rescan of changed files would again write title-only ids beside them.

## Capabilities

### New Capabilities

- `hydration-entity-identity`: how local-metadata hydration derives Entity identity.

### Modified Capabilities

None.
