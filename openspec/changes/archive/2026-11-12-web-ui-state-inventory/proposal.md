# Web UI state inventory and coverage check

Ticket: CORE-1305 (`queue:core:main`).

## Why

Nothing lists every state the web app can be in, so a UI review cannot tell what it missed and
nobody can say whether the browser suite is complete. Corvint's `flows appmap` maps routes only
(41 screens, none of the 19 Console sections) and counts screens as unreached that the specs do
reach. It models no dialogs, menus, tabs, selects, empty, error or loading states, no roles, no
breakpoints, no locales and no accessibility variants. Some routes (`/console/history`,
`/promote`, `/n/:nodeId/all`) are reached by no spec at all, and the viewer, setup and enroll
pages have no goldens.

## What Changes

- Add a repo-owned inventory, `internal/web/app/e2e/lib/ui-states.mjs`, of every web UI state:
  each leaf screen and each in-page state (dialog, menu, tab, select, empty, error, loading, step),
  plus the screen under each role, breakpoint, locale and accessibility variant that applies.
- Browser tests declare the rows they prove with `coverUiStates(testInfo, ...ids)`. Visual-parity
  states count for the rows that name their attachment.
- A Playwright reporter in the unfiltered `make web-ui-e2e-smoke` run joins the inventory to the
  passed tests and the visual-parity attachments. It fails on an uncovered row that is not in the
  reviewed allowlist, on an allowlisted row that is now covered, and on an id that the inventory
  does not define. It writes `ui-state-coverage.json` next to the Playwright output.
- A unit test keeps the inventory well formed and keeps every router leaf and Console section in it.
- Close the largest gaps: reach the routes no spec reached, add goldens for viewer, setup and
  enroll, force error and empty states with `page.route`, and run viewer and manage at phone width.

## Non-goals

- Visual-parity tolerances, or re-recording the archived TECHDUR-WEB-2 baselines.
- Covering every remaining row in this change; the allowlist names what is left.
- Replacing Corvint `flows appmap`; the inventory is the data it can adopt later.

## Capabilities

### New Capabilities

- `web-ui-state-coverage`: the inventory of web UI states and the check that every state is
  covered by a browser test or a visual-parity state, or is listed as a known gap.

### Modified Capabilities

None.
