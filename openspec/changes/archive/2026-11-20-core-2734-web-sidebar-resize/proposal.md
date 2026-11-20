# Resizable, narrower web library sidebar

Ticket: CORE-2734 (`queue:core:main`).

## Why

Testing on a home server, the owner found the web Viewer's library sidebar far wider than its
one-word labels need, with no way to change it. A fixed 236 px column takes space from the page
on laptop screens, and people with long library names want it wider instead.

## What Changes

- The expanded desktop sidebar defaults to 200 px instead of 236 px.
- A separator on the sidebar's inline-end edge resizes it between 168 px and 360 px by pointer
  drag or by keyboard (arrow keys step 16 px, Home and End jump to the limits); a double-click
  resets the default. It carries `role="separator"` with `aria-valuenow`, `aria-valuemin` and
  `aria-valuemax`.
- The chosen width is a per-browser convenience in `localStorage` (`beamfall.sidebarWidth`),
  read and written inside try/catch so a blocked store falls back to the default.

## Non-goals

- The width does not roam with the Profile.
- The collapsed icon rail and the phone drawer keep their fixed widths and get no handle.
