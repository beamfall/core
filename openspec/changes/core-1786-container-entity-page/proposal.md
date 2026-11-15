# Container Entity page: one lockup, a next-up action and grouped children

Ticket: CORE-1786 (`queue:core:main`). Shares the media detail lockup from CORE-1785.

## Why

People browse by the thing they follow, not by every file in it. The web Entity page shows raw
field names, has no primary action, and lists its Media as a flat poster grid, so a container
(an Entity whose Media are ordered) offers no way to carry on where the viewer left off.

## What Changes

- The Entity page header is the media detail stage: the title, a facts chip with the plugin's
  `kindDisplayName` (falling back to the kind), and only viewer-facing fields under their declared
  labels (the CORE-1785 filter). `entity(id)` now selects its existing `kindDisplayName` and
  `fields.label`.
- When the Entity's ordered Media list is shown, one accent-filled 44px action plays or resumes the
  next-up child: the first child in progress, else the first unfinished, else the first. Locked
  children are skipped. A "Next up" caption names it. The player gets the container's order as
  its queue.
- The children render as one ordered list of links, grouped by the item kind in first-seen order
  (a heading per group only when there are several). Wide screens show a tile grid. A phone shows
  list rows: thumbnail, number, title, runtime, progress or watched. DOM order is visual order, and
  no positive tabindex is used. Spacing is 8/12/16/24 with `--radius-card`. The only motion is the
  hover and focus background, removed under reduce-motion.
- Related Entities keep rendering as cards that open their Entity page, before the children.
- Core stays domain-agnostic: no kind is named; labels come from the plugin.

## Not in this change

- Which shelves a library's first screen shows is the plugin's layout (CORE-0199). One card per
  container on the TV library's primary shelf needs the plugin to declare an entity shelf.
- With a plugin detail Layout for the kind, the page renders that Layout and no next-up action. The
  Media list is not fetched in that case.

## Impact

- Web viewer only: `EntityPage.tsx`, `entity-detail.css`, `components/ContainerChildren.tsx`,
  `lib/containerChildren.ts`, the `entity(id)` selection and `EntityInfo` type, and strings in
  `strings/media-detail.ts`. No server or wire-contract change.
