## 1. Tokens

- [x] 1.1 Generator for `tokens.css` and `theme.generated.ts` from the latest published release, with `--check`
- [x] 1.2 `tools/gate/design-token-drift.mjs` runs the generator's drift check; a missing or pre-catalog design-system checkout is skipped loudly
- [x] 1.3 Vitest drift check and golden-vector suite read the sibling design system at test time

## 2. Roles

- [x] 2.1 `theme.css` maps the resolved roles; media surfaces stay on the dark scheme
- [x] 2.2 Raw `oklch()`, hex and Tailwind palette colours in app CSS and TSX move onto roles
- [x] 2.3 `check-colour-literals.mjs` lint wired into `npm run check`, with unit tests

## 3. Run-time themes

- [x] 3.1 `runtime.ts` resolves a preset or theme document and writes the `--theme-*` properties
- [x] 3.2 `RegisterProvider` exposes the scheme preference, preset and `applyThemeDocument`
- [x] 3.3 `ThemePrefsSync` applies the Profile's `theme.preset`; Profile Settings writes it
- [x] 3.4 Screenshots of every shipped preset in light and dark
