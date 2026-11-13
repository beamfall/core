## 1. Inventory

- [x] 1.1 `e2e/lib/ui-states.mjs`: screens, in-page states and context rows seeded from the routers
- [x] 1.2 Unit test: unique ids, every router leaf and Console section present, allowlist well formed

## 2. Check

- [x] 2.1 `coverUiStates` helper and the coverage reporter
- [x] 2.2 Wire the reporter into the unfiltered `make web-ui-e2e-smoke` run
- [x] 2.3 Reviewed allowlist of the rows still uncovered

## 3. Gaps

- [x] 3.1 Reach `/console/history`, `/promote` and `/n/:nodeId/all`
- [x] 3.2 Goldens for viewer, setup and enroll
- [x] 3.3 Error and empty states through `page.route`
- [x] 3.4 Viewer and manage at phone width

## 4. Gate

- [x] 4.1 `openspec validate web-ui-state-inventory --strict`
- [ ] 4.2 `make gate` on the landing head
