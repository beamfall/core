#!/usr/bin/env node
// Design token drift gate (ADR-0106, CORE-1179): the web app's generated token CSS and theme
// presets (internal/web/app/src/styles/tokens.css, src/lib/theme/theme.generated.ts) must equal a
// fresh generation from the latest published beamfall-design-system token release. The generator
// is internal/web/app/scripts/generate-theme-tokens.mjs; this gate runs its --check mode.
//
// The design system is a sibling checkout ($BEAMFALL_DESIGN_SYSTEM_DIR, or
// ../beamfall-design-system); when it is absent the gate is skipped, like core's other
// sibling-only checks. A sibling that predates the theme catalog is skipped the same way, loudly.
import { resolve } from "node:path";
import { pathToFileURL } from "node:url";

const generator = resolve("internal/web/app/scripts/generate-theme-tokens.mjs");
const { designSystemDir, designSystemUnavailable, drift } = await import(
  pathToFileURL(generator).href
);

const dsDir = designSystemDir();
const unavailable = designSystemUnavailable(dsDir);
if (unavailable) {
  console.log(`design-token-drift: SKIPPED (${unavailable}); set BEAMFALL_DESIGN_SYSTEM_DIR`);
  process.exit(0);
}

const stale = drift(dsDir);
if (stale.length) {
  for (const path of stale) console.error(`DRIFT ${path}`);
  console.error("run: npm --prefix internal/web/app run tokens:generate");
  process.exit(1);
}
console.log(`design-token-drift: generated web tokens match the design-system release (${dsDir})`);
