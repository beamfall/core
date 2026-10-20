import { fileURLToPath, URL } from "node:url";
import { defineConfig } from "vitest/config";

// Vitest config kept separate from vite.config.ts so the unit-test runner does
// NOT inherit the production `build`/`server` blocks. Crucially it scopes the
// test `include` to `src/` — the `e2e/` directory holds Playwright specs (run
// via `npm run e2e`), and Playwright's `test.describe()` throws if collected by
// vitest. `passWithNoTests` keeps the gate green until the first
// `src/**/*.test.tsx` unit test lands; new unit tests under src/ are picked up
// automatically. The `@/` alias mirrors vite.config.ts so test imports resolve
// the same way as app code.
export default defineConfig({
  resolve: {
    alias: { "@": fileURLToPath(new URL("./src", import.meta.url)) },
  },
  test: {
    environment: "jsdom",
    globals: true,
    include: ["src/**/*.{test,spec}.{ts,tsx}", "sw-plugin.test.ts"],
    passWithNoTests: true,
  },
});
