# One app-wide poster size, on every pivot and every thumbnail surface

Ticket: CORE-2893 (`queue:core:main`). Source: owner request, 2026-11-21, with a screenshot of
Films > Recommended: the Continue Watching shelf draws large posters and the page offers no way to
make them smaller. Widened the same day by the owner: "the resize should be something that is
univeral across the app" — one preference that every thumbnail surface follows, changeable from any
page that offers the control.

## Why

The poster size control (CORE-2468) sits only in the Library pivot's toolbar and the Folders and
Entity-kind views, and it sets the column floor of poster grids only. Shelf cards keep a fixed
width (164px, 128px on a phone) at every size, and the Collections poster grid, the Categories
cards, the Collections and Categories pivot tiles and the detail cast row keep fixed sizes too. So
the Recommended pivot, which is all shelves, has neither the control nor any effect from it, and
the choice reaches only part of the app.

## What changes

- Web only. Every Library pivot's heading row (Recommended, Library, Collections, Categories)
  shows the same Small / Medium / Large control whenever its pivots show.
- One preference sets every viewer thumbnail surface through custom properties under the root's
  `data-poster-size`: the poster grids' column floors (Library grid, search, See All, Collection,
  Entity and Profile pages), shelf card width on Home, Recommended and detail rows (desktop
  116 / 140 / 164px, phone 92 / 108 / 128px), the Collections poster grid, the Categories cards,
  the Collection and Category tiles on the pivots, and the cast row. Large keeps every surface's
  current size, so the default render does not change.
- Storage is unchanged and already app-wide: one `beamfall.posterSize` value per browser, shared by
  every page and synced across tabs. It was never per Library, so there is nothing to migrate.

## Non-goals

- Native clients.
- Moving the preference into the Profile on the server so it follows a person across browsers and
  devices (CORE-2468 kept it per browser because the right size depends on the screen). A separate
  ticket if wanted.
- A control on Home.

## Failure modes

- Same as CORE-2468: blocked storage keeps the choice for the session; an unknown value reads as
  Large.
