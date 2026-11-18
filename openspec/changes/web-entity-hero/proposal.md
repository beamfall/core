# Hero for parent-entity detail pages in the web viewer

Ticket: CORE-2441 (`queue:core:main`), web only.

## Why

A parent Entity page (`/entity/:id`) shows a poster, the kind chip and a definition list of
fields, then every related group as a plain poster grid. Comparable media servers open a parent
page with a backdrop, poster, facts line, overview, a next-up label and unwatched count, then rows
of children, people and related items. The data for most of that is already on `entity(id)`:
artwork slots, declared fields and related Entities with engagement counts.

## What Changes

- The entity page header becomes the detail stage the Media page uses: the `backdrop` artwork slot
  behind a scrim, the poster (or a person's headshot), the title, and a facts line built from the
  kind label, the `year` field, the rating fields, the community score and up to three
  category-archetype related Entities.
- A `summary` field (the builtin Media field name) renders as the clamped overview instead of a
  definition-list row.
- The poster carries an unwatched-count badge: the unfinished playable Media the page lists, else
  the sum of the child containers' remaining counts.
- The next-up label names the child Play starts, with its plugin subtitle when it has one.
- Related groups render in a fixed order after the header: container groups as a poster row whose
  cards show "N items" from the child's total count, then the page's own Media list, then person
  groups as the headshot cast row, then any other groups.
- Nothing degrades when a slot or field is absent: no backdrop, no overview, no badge, no row.

## Non-goals

- No schema or wire change; no new server field.
- No plugin-specific labels or field names beyond the builtin `summary` and `year` names and the
  rating fields the Media page already reads.

## Impact

`internal/web/app/src/areas/viewer/pages/EntityPage.tsx`, a new `lib/entityHero.ts` helper, a
shared `components/DetailStage.tsx` (backdrop and overview), entity-detail CSS and strings.
