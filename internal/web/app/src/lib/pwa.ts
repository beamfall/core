// PWA service-worker registration (ADR-0007 §1: installable PWA = manifest +
// a shell-only service worker). The worker (`sw.js`) is emitted at build time
// by the `beamfall-sw` Vite plugin (see vite.config.ts); it precaches the built
// app shell, serves hashed assets cache-first and navigations network-first,
// while ALWAYS bypassing `/api/*` and media so the cache can never serve stale
// library or playback data.
//
// Registration is dev-safe: the worker only exists in the production bundle, so
// in `npm run dev` the fetch 404s and we skip registration silently.
//
// Upgrade handoff: the worker calls skipWaiting + clients.claim, so after a
// server upgrade the new worker takes over this page mid-session. The page's
// lazy chunks then resolve against a cache that no longer holds them, so the
// first `controllerchange` that REPLACES a live controller reloads the page
// exactly once. A first install (no previous controller) never reloads.

interface ServiceWorkerHost {
  controller: ServiceWorker | null;
  addEventListener(type: "controllerchange", listener: () => void): void;
}

export function reloadOnceOnControllerChange(host: ServiceWorkerHost, reload: () => void): void {
  if (!host.controller) return;
  let reloaded = false;
  host.addEventListener("controllerchange", () => {
    if (reloaded) return;
    reloaded = true;
    reload();
  });
}

export function registerServiceWorker(): void {
  if (typeof navigator === "undefined" || !("serviceWorker" in navigator)) {
    return;
  }
  // BASE_URL is "/web/" — the worker is emitted at the bundle root and its
  // scope is the same base, so it controls every client route under /web/.
  const base = import.meta.env.BASE_URL;
  const swUrl = `${base}sw.js`;
  reloadOnceOnControllerChange(navigator.serviceWorker, () => window.location.reload());
  window.addEventListener("load", () => {
    navigator.serviceWorker.register(swUrl, { scope: base }).catch(() => {
      // No worker in dev (or registration blocked) — the app works uncached.
    });
  });
}
