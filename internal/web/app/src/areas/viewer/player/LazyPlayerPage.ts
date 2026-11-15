import { lazyRoute } from "@/lib/lazyRoute";

// The player is the only viewer route that pulls in hls.js, so it stays a
// separate chunk. It lives in its own module so preload.ts (imported by the
// media cards) can warm it without importing the viewer area root.
export const LazyPlayerPage = lazyRoute(() =>
  import("../pages/PlayerPage").then((m) => ({ default: m.PlayerPage })),
);

// The same chunk's player engine, mounted by the persistent player host above
// the viewer routes (CORE-1633) so audio outlives the watch route.
export const LazyHostedPlayer = lazyRoute(() =>
  import("../pages/PlayerPage").then((m) => ({ default: m.HostedPlayer })),
);
