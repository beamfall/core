# Name every Select trigger and give each route one h1

Ticket: CORE-0089 (`queue:core:main`), roadmap item DESAUDIT0927-CWEB-A11Y-NAMES-1.

## Why

The 2026-09-27 design audit found `<SelectTrigger>` buttons with no accessible name (axe `button-name`,
WCAG 2 A, critical) on Manage operations and unmatched, and most Manage and Viewer routes render no
`h1`, so screen-reader heading navigation has no page title.

## What Changes

- Each unnamed Select trigger in the Manage and Console areas gets a localized `aria-label` (existing strings
  where one fits; one new key, `scheduleUnit`, in all five locales).
- The Manage shell renders one visually hidden `h1` naming the current route. Manage section titles
  keep their markup and styling and become level-2 headings.
- The Console shell renders one visually hidden `h1` naming the current Console route, ahead of the
  admin gate. Console `Section` titles stay level-2 headings.
- The player and browse pages render one visually hidden `h1` (the item or collection title, or the
  brand when none is loaded). The profile picker, entity and see-all pages already render a visible
  `h1`; the e2e now proves one `h1` on `/watch`, `/n`, `/entity`, `/seeall` and `/profiles` as well. The leak-safe unavailable state
  (an entity that resolves to nothing) renders its title as the `h1`.

## Non-goals

- Visible layout, styling or golden changes.

## Failure modes

- No route title is known: the Manage `h1` falls back to the Manage title; the viewer `h1` to the brand.

## Acceptance evidence

- `e2e/a11y-browser.spec.ts`: zero `button-name` violations on manage/operations and
  manage/unmatched at 1440x900 and 390x844 and on every Console route; exactly one `h1` per Manage,
  Console and Viewer route tested.
- `strings.test.ts` pins updated for the new key.
- Release evidence `DESAUDIT0927-CWEB-A11Y-NAMES-1.md`, retained outside this repository.

## Rollback

Revert the change. It holds no stored state and changes no wire contract.

## Capabilities

### New Capabilities

- `web-a11y-names-headings`: accessible names for form triggers and the single-h1 page rule.

### Modified Capabilities

None.
