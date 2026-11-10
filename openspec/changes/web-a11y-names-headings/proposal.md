# Name every Select trigger and give each route one h1

Ticket: CORE-0089 (`queue:core:main`), roadmap item DESAUDIT0927-CWEB-A11Y-NAMES-1.

## Why

The 2026-09-27 design audit found `<SelectTrigger>` buttons with no accessible name (axe `button-name`,
WCAG 2 A, critical) on Manage operations and unmatched, and most Manage and Viewer routes render no
`h1`, so screen-reader heading navigation has no page title.

## What Changes

- Each unnamed Select trigger in the Manage area gets a localized `aria-label` (existing strings
  where one fits; one new key, `scheduleUnit`, in all five locales).
- The Manage shell renders one visually hidden `h1` naming the current route. Manage section titles
  keep their markup and styling and become level-2 headings.
- The player and browse pages render one visually hidden `h1` (the item or collection title, or the
  brand when none is loaded).

## Non-goals

- Console area routes and Select triggers (`areas/console/**`) are owned by another lane and are
  listed as follow-ups.
- Visible layout, styling or golden changes.

## Failure modes

- No route title is known: the Manage `h1` falls back to the Manage title; the viewer `h1` to the brand.

## Acceptance evidence

- `e2e/a11y-browser.spec.ts`: zero `button-name` violations on manage/operations and
  manage/unmatched at 1440x900 and 390x844; exactly one `h1` per Manage and Viewer route.
- `strings.test.ts` pins updated for the new key.
- `docs/release/evidence/DESAUDIT0927-CWEB-A11Y-NAMES-1.md`.

## Rollback

Revert the change. It holds no stored state and changes no wire contract.

## Capabilities

### New Capabilities

- `web-a11y-names-headings`: accessible names for form triggers and the single-h1 page rule.

### Modified Capabilities

None.
