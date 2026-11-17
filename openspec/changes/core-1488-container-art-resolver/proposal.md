# Resolve the container Entities a Media belongs to, for local container art

Ticket: CORE-1488 (`queue:core:main`).

## Why

The scan already discovers container sidecars (`season01-poster.jpg`) but imports none: the
`scan.ContainerEntityResolver` seam has no implementation, so on a library laid out as
`Show/Season 01/S01E01.mkv` the show and season pages have no poster even when the folder holds one.

## What Changes

- `identify.Service` resolves a Media's ordered container Entities, innermost first: the container
  Entity that holds the Media through a single-holder relation, then each container Entity it
  points at, using only kinds the Library's plugin declares as containers. A holder's ordinal is
  the sort order of its relation to the next container. Every Entity is read inside the Media's
  Library, and the chain is depth-bounded.
- The scan seam returns the ordered list. The innermost ordered container keeps the numbered
  sidecar rule (`season<NN>-poster` in the Media's folder or its parent, inside the root). The
  outermost container takes the folder `poster`, `folder` or `cover` image from the Library's
  top-level folder that holds the Media; a Media directly in the root, or a folder outside it,
  gives nothing.
- The composition root's scan identifier forwards the seam to the service.

## Non-goals

- No wire change, no plugin API change, no new Entity writes: the resolver only reads Entities
  that identification or hydration already wrote.
