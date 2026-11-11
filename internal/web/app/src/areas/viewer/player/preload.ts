import { LazyPlayerPage } from "./LazyPlayerPage";

// Warms the lazy PlayerPage chunk (and hls.js with it) before the viewer
// presses play. It resolves the same lazyRoute() the /watch route renders, so
// once it settles the route renders the page without suspending. Idempotent and
// best-effort: a failed prefetch is forgotten so a later hover can try again,
// and the route load surfaces any real failure.
export function preloadPlayer(): void {
  LazyPlayerPage.preload().catch(() => {});
}
