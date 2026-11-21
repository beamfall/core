# Poster size on a Library's Recommended pivot and its shelves

Ticket: CORE-2893 (`queue:core:main`). Source: owner request, 2026-11-21, with a screenshot of
Films > Recommended: the Continue Watching shelf draws large posters and the page offers no way to
make them smaller.

## Why

The poster size control (CORE-2468) sits only in the Library pivot's toolbar and the Folders and
Entity-kind views, and it sets the column floor of poster grids only. Shelf cards keep a fixed
width (164px, 128px on a phone) at every size, so the Recommended pivot, which is all shelves, has
neither the control nor any effect from it.

## What changes

- Web only. The Recommended pivot's heading row shows the same Small / Medium / Large control
  whenever its pivots show (a Library with content).
- Shelf card width follows the same `data-poster-size` preference through a `--bf-shelf-card`
  custom property beside `--bf-grid-min`: desktop 116 / 140 / 164px, phone 92 / 108 / 128px.
  Large keeps today's widths, so the default render does not change.
- The preference is unchanged: still one choice per browser (CORE-2468 kept it out of the Profile
  because the right size depends on the screen). Every viewer shelf follows it, Home included, as
  every viewer grid already does.

## Non-goals

- Native clients.
- Moving the preference to the Profile or to each Library.
- A control on Home.

## Failure modes

- Same as CORE-2468: blocked storage keeps the choice for the session; an unknown value reads as
  Large.
