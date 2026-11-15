# Beamfall React Components

This is the internal web component kit. Use it before adding page-local
Workbench or Viewer markup.

Source chain:

`../beamfall-design-system/mockups` -> `docs/design-system.md` / `docs/design`
-> `src/styles/tokens.css` -> these React components -> page implementations.

Agents should change the source first for visual changes. Only add page-local
markup when no kit component fits and at least two screens do not already share
the shape.

## Workbench

Import Manage and Console presentation components from:

```ts
import { ManageSection, ManageTable, StatusBadge } from "@/components/beamfall/workbench";
```

Do not import from area-local wrappers such as `areas/console/ui` or
`areas/manage/components/ManageSection`; those shims are gone.

Use:

- `ManageSection` for Manage panels.
- `ManageTable` for Manage tables; keep headers and rows page-local until a
  real repeated table shape emerges.
- `ManageInlineForm` and `ManageField` for compact add/filter forms.
- `StatusBadge` for Manage status pills.
- `StatusChip`, `DataTable`, `EmptyState`, `ErrorState`, `Fieldset`, and
  `ControlRow` for Console Workbench sections.

## Surface

Full-page surfaces outside the app chrome (first-run setup, enroll, promote,
the admin unlock gates) compose:

```ts
import { GlassCard, PageShell, SurfaceHeading } from "@/components/beamfall/surface";
```

- `PageShell` for the cinema backdrop, brand bar and the one centred column.
- `GlassCard` for the column's card (`asChild` makes a `<form>` the card).
- `SurfaceHeading` for the card title and lead.
- `FieldLabel`, `FieldLegend`, `FieldInput`, `FieldHint` and `FieldButton` for
  fields; `PrimaryAction` and `SecondaryAction` for the card's buttons.
- `HairlineRows`, `HairlineList`, `OptionRow` and `OptionRadio` for choice
  rows; `SurfaceSection` and `SurfaceInset` for blocks inside the card.

Theming:

- Components use semantic CSS hooks (`manage-*`, `console-*`) and tokens from
  `src/styles/tokens.css`.
- Do not add raw color values in component JSX.
- If CSS needs a new color, add or map a token first.

Testing:

- Add or update a page test when behavior changes.
- Add a kit smoke test when a component's semantic contract changes.
- Run `npm --prefix internal/web/app run typecheck`.
- Run `npm --prefix internal/web/app test`.
- Run `npm --prefix internal/web/app run build` after source changes before
  compiling the Go server.
