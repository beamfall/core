# Related Entity groups carry their relation direction

Ticket: CORE-2036 (`queue:core:main`).

## Why

`entity(id)` lists related Entities from both relation directions in one group per kind, and
nothing says which way each group relates. The web Entity page plays a container with no Media of
its own through its next-up child container, the first related container group. In a three-level
container graph the middle container's parent group can sort first and be played instead of a
child.

## What Changes

- A relation points from the Entity whose kind declares it to the Entity it names, so the Entities
  pointing at the context Entity are its children and the Entities it points at are its parents.
- Each related group carries a domain-neutral `direction`, `child` or `parent`. Groups are formed
  per kind and direction, ordered by kind and then children before parents. An Entity linked both
  ways is listed once, as a parent.
- The web next-up child container reads only child groups.

## Impact

- Code: `internal/store` (`RelatedEntityGroups`), `internal/graph` (`RelatedEntityGroupInfo.direction`),
  web `nextUpChildContainer` and the Entity query.
- Wire: additive field `RelatedEntityGroup.direction`; wire goldens regenerated. The contract needs a
  published minor version and consumer repins by the lander.
- No migration.
