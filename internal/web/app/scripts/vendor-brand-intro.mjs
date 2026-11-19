#!/usr/bin/env node
// Vendors the Cut Beam logo intros the web splash plays inline (CORE-2556) from the
// beamfall-design-system brand assets (CORE-2555). The SVGs are copied byte for byte; the
// splash renders the design system's own animation, never a port of it.
//
//   node scripts/vendor-brand-intro.mjs           # copy the intros into src/assets/brand/intro/
//   node scripts/vendor-brand-intro.mjs --check   # exit 1 when a vendored copy has drifted
//
// The design system is the same sibling checkout the token generator reads:
// $BEAMFALL_DESIGN_SYSTEM_DIR, or ../beamfall-design-system next to the core repository root.

import { existsSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const APP_DIR = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const REPO_ROOT = resolve(APP_DIR, "../../..");
const SOURCE_DIR = "assets/brand/intro";

export const VENDOR_DIR = join(APP_DIR, "src/assets/brand/intro");
export const INTROS = ["beamfall-lockup-intro.svg", "beamfall-stacked-intro.svg"];

export function designSystemDir(env = process.env) {
  return resolve(env.BEAMFALL_DESIGN_SYSTEM_DIR || join(REPO_ROOT, "../beamfall-design-system"));
}

/** Why `dsDir` cannot supply the intros, or null when it can. */
export function introSourceUnavailable(dsDir) {
  if (!existsSync(dsDir)) return `no design-system checkout at ${dsDir}`;
  const missing = INTROS.filter((name) => !existsSync(join(dsDir, SOURCE_DIR, name)));
  if (missing.length)
    return `${dsDir} predates the brand intros (CORE-2555): ${missing.join(", ")}`;
  return null;
}

/** Vendored intro paths whose bytes differ from the design system's. */
export function introDrift(dsDir) {
  return INTROS.filter((name) => {
    const vendored = join(VENDOR_DIR, name);
    if (!existsSync(vendored)) return true;
    return !readFileSync(vendored).equals(readFileSync(join(dsDir, SOURCE_DIR, name)));
  }).map((name) => join(VENDOR_DIR, name));
}

if (import.meta.url === `file://${process.argv[1]}`) {
  const dsDir = designSystemDir();
  const unavailable = introSourceUnavailable(dsDir);
  if (unavailable) {
    console.error(`vendor-brand-intro: ${unavailable}; set BEAMFALL_DESIGN_SYSTEM_DIR`);
    process.exit(1);
  }
  if (process.argv.includes("--check")) {
    const stale = introDrift(dsDir);
    for (const path of stale) console.error(`DRIFT ${path}`);
    process.exit(stale.length ? 1 : 0);
  }
  for (const name of INTROS) {
    writeFileSync(join(VENDOR_DIR, name), readFileSync(join(dsDir, SOURCE_DIR, name)));
    console.log(`vendored ${name}`);
  }
}
