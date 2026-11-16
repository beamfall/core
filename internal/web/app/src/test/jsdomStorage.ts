// Vitest setup file (CORE-2185): give the jsdom test environment jsdom's Web
// Storage on every supported Node major.
//
// Node 25+ defines its own global `localStorage` and `sessionStorage`. Without
// `--localstorage-file` they read as `undefined` (with an ExperimentalWarning).
// Vitest's jsdom environment copies window properties onto the global only
// when the global does not already have them, so on Node 25+ the tests see
// Node's unusable storage instead of jsdom's. Node 22 has no such globals, so
// jsdom's storage is copied as expected. Re-pointing both names at the jsdom
// window makes the two behave the same. Vitest exposes that window as
// `globalThis.jsdom.window` in the jsdom environment.

type StorageKey = "localStorage" | "sessionStorage";

const dom = (globalThis as { jsdom?: { window: Record<StorageKey, Storage> } }).jsdom;

if (dom) {
  for (const key of ["localStorage", "sessionStorage"] as const) {
    if (globalThis[key] !== dom.window[key]) {
      Object.defineProperty(globalThis, key, {
        configurable: true,
        enumerable: true,
        get: () => dom.window[key],
      });
    }
  }
}
