# Fold edition groups in discovery rails

Ticket: CORE-2467.

## Why

The Related and More-with rails can recommend an edition of the Media being viewed,
including its parent and sibling editions, and can show multiple cards for one group.

## What Changes

- Fold `entity-media` rail reads that pass non-empty `excludeEditionGroupOf` in SQL
  before pagination. Prefer the visible parent;
  if the parent is hidden by Library, reveal or certification filtering, use the visible
  edition with the lowest Media ID. Trashed Media cannot represent a group.
- Add optional `excludeEditionGroupOf` to the Source read. It opts into folding and excludes
  the visible context Media, its parent and sibling editions before paging. Related and web
  More-with pass it.
- Keep plain `entity-media` reads without `excludeEditionGroupOf` unchanged for container,
  person and category pages, matching `MediaForEntity` and ordered-container playback seeds.
  Keep `all-items` Library browse unchanged. Whether browse and Entity pages should fold
  editions is a separate decision.
- Read relations and visibility predicates from the appropriate catalog or encrypted plane.

For opted-in rail reads, groups follow connected `edition-of` relations, including chains.
A visible terminal parent wins; ties between parents and groups without a visible parent use the lowest Media ID.
The representative is independent of the queried credit, preventing duplicates when Related
combines multiple credits. At least one visible member must carry the queried credit.
Profile Mute suppression continues after representative selection, with no edition fallback.
Direct Entity lists using `MediaForEntity` also retain their existing behavior.

The optional wire argument requires regenerated goldens and a local client-core-wire 2.27.0
artifact and registry update; no external release or consumer major-version change is needed.

## Verification

Regression coverage for parent, edition and sibling contexts; one card per group; hidden
and trashed parents; certification filters; encrypted planes; paging; unchanged playback
seeds and plain Entity Source reads, including an edition whose parent is not in the Entity.
Focused Go, conformance, lint, web and OpenSpec checks.
