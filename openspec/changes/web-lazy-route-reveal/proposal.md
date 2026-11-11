# Reveal resolved lazy routes without the Suspense delay

Ticket: CORE-0976 (`queue:core:main`).

## Why

Every cold load of `/web/` and the first open of the player waited about 300 ms after the route
chunk had loaded. A `React.lazy` component that is not yet resolved at first read commits the
Suspense fallback, and React 19 holds the resolved content back for a throttle window after the
fallback was shown. The section and playback-info requests only start once the content is revealed.

## What Changes

- Lazy route components remember their resolved module and render it directly once it is available,
  so a preloaded route never suspends.
- The entry resolves the matched initial area chunk (bounded by a short timeout) before the first
  render.
- `preloadPlayer()` resolves the same lazy player component that the route renders.
- A chunk that is not resolved still renders the route fallback.
