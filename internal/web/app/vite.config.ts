import { fileURLToPath, URL } from "node:url";
import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";
import { beamfallShellSW } from "./sw-plugin";

// The bundle is served by the Go server via go:embed. Post-cutover it is the
// canonical app at /web/. Output lands in the ignored app-dist/production
// subtree; web.go falls back to the tracked placeholder in a standalone clone.
export default defineConfig({
  base: "/web/",
  plugins: [react(), tailwindcss(), beamfallShellSW({ base: "/web/" })],
  resolve: {
    alias: { "@": fileURLToPath(new URL("./src", import.meta.url)) },
  },
  build: {
    outDir: "../app-dist/production",
    emptyOutDir: true,
    // The embedded production bundle stays lean (no sourcemap committed into
    // the Go binary); `npm run dev` keeps full sourcemaps for debugging.
    sourcemap: false,
    rollupOptions: {
      output: {
        // Split large, independently-cacheable vendors into their own chunks so
        // a bump to one (e.g. a Radix patch) doesn't bust the others' cache, and
        // so the player-only hls.js never rides in the initial/index chunk — it
        // is reached only through the lazily-loaded Viewer player route.
        manualChunks(id) {
          if (!id.includes("node_modules")) return undefined;
          // hls.js (~150 KB) is player-only; keep it in its own chunk so it
          // loads only when the player route is hit.
          if (id.includes("/hls.js/")) return "hls";
          // The React runtime: shared by every route, cached across deploys.
          if (
            id.includes("/react/") ||
            id.includes("/react-dom/") ||
            id.includes("/react-router/") ||
            id.includes("/scheduler/")
          ) {
            return "react-vendor";
          }
          // Radix primitives + the shadcn/UI vendor that wraps them.
          if (
            id.includes("/@radix-ui/") ||
            id.includes("/lucide-react/") ||
            id.includes("/class-variance-authority/") ||
            id.includes("/clsx/") ||
            id.includes("/tailwind-merge/")
          ) {
            return "ui-vendor";
          }
          return undefined;
        },
      },
    },
  },
  server: {
    // `npm run dev` proxies API + media calls to a local server binary so the
    // React app talks to the real GraphQL/manifest/SSE surfaces.
    proxy: {
      "/api": "http://127.0.0.1:8080",
    },
  },
});
