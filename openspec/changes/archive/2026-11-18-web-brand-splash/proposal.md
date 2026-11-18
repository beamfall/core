# Cold-load logo intro splash in the web app

Ticket: CORE-2556 (`queue:core:main`), the web part of the splash on every platform (CORE-2555 to
CORE-2560).

## Why

Owner decision 2026-11-18: every Beamfall client plays the Cut Beam logo intro as its splash on
cold launch, with the lockup matching the screen. The web app opens straight onto its first screen
with no brand moment, and the design system now ships the intros (CORE-2555).

## What Changes

- `index.html` carries both design-system intros inline from the first paint, each in its own
  declarative shadow root (the two SVGs share ids, classes and keyframe names). A Vite plugin,
  `brand-splash-plugin.ts`, injects them in place of a marker, with the splash layout CSS.
- Width at least height shows the horizontal lockup intro, otherwise the stacked one. Both run from
  the same moment and the aspect ratio only toggles visibility, so a rotation or resize mid-intro
  switches layout at the same point in the intro.
- Under `prefers-reduced-motion` the intros' own stylesheets show the static lockup.
- `src/lib/brandSplash.ts` hands off: once every intro animation has finished (capped at 4 s for
  a held-back tab) and the first screen has rendered, the splash fades out and is removed. It
  plays once per document load: in-app navigation and back/forward-cache restores never replay it.
- The SVGs are vendored byte for byte under `src/assets/brand/intro/` by
  `scripts/vendor-brand-intro.mjs`; the design-token drift gate also fails when they differ from
  the design-system sibling (skipped, loudly, when the sibling predates CORE-2555).
- Visual-parity screenshots wait for the handoff so goldens keep capturing the UI.

## Non-goals

- Native clients (CORE-2557 to CORE-2560) and the JS reference player: the web plays the SVGs.
- A per-theme recolour beyond the light-surface wordmark colour.

## Failure modes

- A browser without declarative shadow DOM shows the plain background until the app renders.
- The splash never blocks input (`pointer-events: none`) and is `aria-hidden`.

## Acceptance evidence

- E2E `e2e/brand-splash.spec.ts` (`unauth` project, in the web smoke): landscape plays the
  horizontal intro, portrait the stacked one, a mid-intro rotation switches layout without
  restarting either clock, reduced motion shows the static lockup in both layouts, and each hands
  off to the first screen.
- vitest: `brand-splash-plugin.test.ts`, `src/lib/brandSplash.test.ts`.

## Rollback

Revert the change. No stored state is added.
