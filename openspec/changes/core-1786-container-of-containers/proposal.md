# Container of containers plays on; empty detail Layouts stay quiet (CORE-1786)

## Why

CORE-1786 asks that a container Entity page carry one primary action for its next-up child. On the
synthetic corpus a container whose children are themselves containers (a TV show of seasons) opened
with no primary action: its plugin declares a detail Layout, so the page never read the Entity's own
Media, and it has none anyway. The same page showed the library onboarding empty state ("Start
first scan", "Migrate") because the detail Layout's only Section was empty for it.

## What changes

- The Entity page reads the Entity's own Media for the primary action whether or not a detail
  Layout renders them. The hand-rolled Media list still renders only without a detail Layout.
- With no playable Media of its own, the page plays on through the next-up child container: the
  first related group whose archetype is a container, then its first item with children left (else
  its first), and that child's next-up Media, with that child's order as the queue. No kind name
  is read.
- A detail Layout whose Sections are all empty renders nothing inside an Entity page, never the
  library onboarding empty state.

## Impact

`internal/web/app/src/areas/viewer` (EntityPage, LayoutView, containerChildren) and their Vitest.
