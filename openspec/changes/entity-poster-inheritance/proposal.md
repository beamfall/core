# Give Entities without provider artwork a poster from their own Media

Ticket: CORE-1211 (`queue:core:main`).

## Why

The only writer of an Entity poster is provider identification (`ImportEntityPoster` from
`internal/identify`). A local, tagged music Library therefore shows every album and every artist
without artwork, although each track already has a poster extracted from its embedded cover.

## What Changes

- A plugin wiring may declare, per Entity kind, where a poster is inherited from, in order:
  the kind's member Media through a named relation, or Entities of another kind that point at it
  through a named relation. Kinds without a declaration never inherit (a person Entity does not
  get a film poster).
- The artwork sweep (already queued after every scan and at startup) gains an Entity pass after the
  Media pass. For each Entity of a declared kind without a poster it copies the first source poster
  found and marks the copy as inherited. A server-generated fallback poster (a frame grab) is not a
  cover and is never inherited.
- Music declares: an album inherits from its tracks in track order; an artist inherits from an
  album that names it as album artist, else from a track it appears on.
- Provider artwork still wins: identification treats an inherited poster as absent, and importing
  an Entity poster removes the inherited mark, after which the sweep never touches it.
- Steady state writes nothing: an Entity with its own poster costs one stat; an inherited poster
  costs one relation read and a stat of its recorded source. Nothing is re-hashed or re-copied per
  scan. An inherited poster is replaced only when its recorded source poster changed or is no
  longer one of the Entity's sources (a track re-filed under another album), and is removed when
  no source has a poster any more.

## Non-goals

- `cover.jpg` / `folder.jpg` sidecars. The per-item pipeline extracts embedded art only today;
  when it learns sidecars, albums inherit them with no further change.
- Encrypted or Sealed Libraries: they have no plaintext Media posters, so nothing is inherited.
- Selected-Media sweeps; the global sweep that follows every scan runs the Entity pass.
- Choosing the "best" album for an artist. The first album by title with a poster is used and
  stays in place while it is valid.

## Failure modes

- A copy that fails (disk full, source removed mid-pass) leaves the Entity without a poster and is
  retried on the next sweep; a partial file is never published (atomic rename).
- An Entity whose sources have no poster costs one or two relation reads per sweep until one does.

## Acceptance evidence

- `internal/artwork/inherit_test.go`: `TestSweepInheritsEntityPosterFromMemberMedia`,
  `TestSweepInheritsEntityPosterFromRelatedEntity`, `TestInheritedEntityPosterYieldsToImportedPoster`,
  `TestSweepKeepsImportedEntityPoster`, `TestSweepInheritanceIsIdempotent`,
  `TestSweepRefreshesInheritedPosterWhenSourceChanges`, `TestSweepDropsInheritedPosterWhenSourceLeaves`,
  `TestSweepNeverInheritsGeneratedPoster`, `TestSweepSkipsUndeclaredKinds`.
- `internal/identify`: `TestEntityPosterReplacesInheritedPoster`.
- `cmd/beamfall/hooks_test.go`: `TestMusicDeclaresEntityPosterInheritance`.

## Rollback

Revert the change. Inherited posters stay on disk as ordinary Entity posters; delete
`<data>/artwork/entity/*/poster.jpg` that have a `poster.jpg.inherited` sibling to remove them.

## Capabilities

### New Capabilities

- `entity-artwork-inheritance`: when and how an Entity takes a poster from related Media or Entities.

### Modified Capabilities

None.
