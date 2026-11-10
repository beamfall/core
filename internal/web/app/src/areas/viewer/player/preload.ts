// Warms the lazy PlayerPage chunk (and hls.js with it) before the viewer
// presses play. It imports the same module as the lazy() route in
// viewer/index.tsx, so the bundler emits one shared chunk and the route's own
// import resolves from the module cache. Idempotent and best-effort: a failed
// prefetch is forgotten so a later hover can try again, and the route load
// surfaces any real failure.
let playerChunk: Promise<unknown> | null = null;

export function preloadPlayer(): void {
  if (playerChunk) return;
  playerChunk = import("../pages/PlayerPage").catch(() => {
    playerChunk = null;
  });
}
