## Why

CORE-2928. On a cold load the web app renders its first screen in about 110 ms, but the splash
from CORE-2556 covers it until about 1.5 s: the handoff waits for the whole logo intro (about
1.25 s) and then the 240 ms fade. Owner decision 2026-11-21: the splash leaves as soon as the app
is ready, so the intro only plays while the app is genuinely loading.

## What Changes

- The splash fades out once the first screen has rendered, cutting the intro short when the app
  is faster than it.
- The 4 s cap on a held-back intro goes: the splash no longer waits on the intro at all.
- Reduced motion keeps the static lockup until the first screen is ready, as before.

## Impact

- `internal/web/app/src/lib/brandSplash.ts` and its vitest.
- `internal/web/app/e2e/brand-splash.spec.ts` holds the bundle back so the intro can be inspected.
