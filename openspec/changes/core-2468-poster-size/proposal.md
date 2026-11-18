# A poster size control for the web viewer's grids

Ticket: CORE-2468 (`queue:core:main`). Source: owner request, 2026-11-18 (parity with Plex's grid
size control). The TV Library on the Tower draws 3 posters per row at about 965px wide.

## Why

Every web poster grid uses a fixed 150px column floor (96px on a phone), so a large Library shows
few titles per screen and there is no way to see more of it at once.

## What changes

- Web only. A Small / Medium / Large poster size, Large being today's size and the default.
- The control sits in the Library toolbar beside sort and filters, in the Media view, the Entity
  kind views and the Folders view.
- One choice per browser, kept in localStorage like the Library display mode (the right size
  depends on the screen, so it is not a Profile preference). It applies to every viewer poster
  grid: Library views, Collections, show and season item grids, See All and search.
- The root element carries `data-poster-size`; chrome.css sets the grid column floor from it
  (desktop 110 / 130 / 150px, phone 72 / 84 / 96px, wide thumbnails scaled alike).
- At Small, card images hint a 160px slot so a 2x screen fetches the w320 derivative.

## Non-goals

- Native clients (Apple, Android): separate tickets.
- A continuous slider: three steps keep the layout predictable and testable.

## Failure modes

- Storage blocked (private window): the choice holds for the session and falls back to Large later.
- An unknown stored value reads as Large.
