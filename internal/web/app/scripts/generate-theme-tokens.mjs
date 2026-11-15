#!/usr/bin/env node
// Generates the web app's token CSS and shipped theme presets from the latest published
// beamfall-design-system token release (ADR-0106, ADR-0231 D7/D10, CORE-1179).
//
//   node scripts/generate-theme-tokens.mjs           # write src/styles/tokens.css + theme.generated.ts
//   node scripts/generate-theme-tokens.mjs --check   # exit 1 when either file has drifted
//
// The design system is a sibling checkout: $BEAMFALL_DESIGN_SYSTEM_DIR, or
// ../beamfall-design-system next to the core repository root. Only derived values leave it:
// the release CSS (published for clients to vendor) and each preset's resolved role values,
// computed here by the app's own resolver. Preset documents, the resolver spec and the golden
// vectors stay in the design system.

import { existsSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import {
  compileBasePalettes,
  isThemeRejection,
  paletteToHex,
  resolveTheme,
  THEME_ROLES,
} from "../src/lib/theme/resolver.ts";

const APP_DIR = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const REPO_ROOT = resolve(APP_DIR, "../../..");

export const OUTPUTS = {
  css: join(APP_DIR, "src/styles/tokens.css"),
  ts: join(APP_DIR, "src/lib/theme/theme.generated.ts"),
};

export function designSystemDir(env = process.env) {
  return resolve(env.BEAMFALL_DESIGN_SYSTEM_DIR || join(REPO_ROOT, "../beamfall-design-system"));
}

/**
 * Why the checkout at `dsDir` cannot feed the generator, or null when it can. A checkout older
 * than the theme preset catalog (CORE-1177) counts as unusable, not as drift.
 */
export function designSystemUnavailable(dsDir = designSystemDir()) {
  if (!existsSync(join(dsDir, "tokens/releases/index.json"))) {
    return `no beamfall-design-system checkout at ${dsDir}`;
  }
  for (const file of ["tokens/theme/presets.json", "tokens/theme/vectors.json"]) {
    if (!existsSync(join(dsDir, file))) {
      return `beamfall-design-system checkout at ${dsDir} has no ${file} (it predates the theme catalog; update it)`;
    }
  }
  return null;
}

const readJson = (path) => JSON.parse(readFileSync(path, "utf8"));

function shadows(css, selector) {
  const block = new RegExp(
    `(?:^|\\n)${selector.replaceAll(".", "\\.")} \\{([\\s\\S]*?)\\n\\}`,
  ).exec(css);
  if (!block) throw new Error(`release CSS has no ${selector} block`);
  const pick = (name) => {
    const m = new RegExp(`--${name}:\\s*([^;]+);`).exec(block[1]);
    if (!m) throw new Error(`release CSS ${selector} has no --${name}`);
    return m[1].trim();
  };
  return { card: pick("shadow-card"), window: pick("shadow-window") };
}

/** Builds both outputs from the design-system checkout at `dsDir`. */
export function generate(dsDir = designSystemDir()) {
  const tokensDir = join(dsDir, "tokens");
  const index = readJson(join(tokensDir, "releases/index.json"));
  const release = index.latestPublished;
  const releaseDir = join(tokensDir, "releases", release);
  const releaseCss = readFileSync(join(releaseDir, "beamfall-tokens.css"), "utf8");
  const tokens = readJson(join(releaseDir, "beamfall-tokens.json"));
  const catalog = readJson(join(tokensDir, "theme/presets.json"));

  const base = compileBasePalettes(tokens.theme);
  const presets = catalog.presets.map((preset) => {
    const resolved = resolveTheme([preset.document], {}, base);
    if (isThemeRejection(resolved)) {
      throw new Error(`preset ${preset.id} is rejected: ${JSON.stringify(resolved.error)}`);
    }
    return { id: preset.id, nameKey: preset.name_key, dark: resolved.dark, light: resolved.light };
  });
  const fallback = presets.find((p) => p.id === catalog.default_preset);
  if (!fallback) throw new Error(`default preset ${catalog.default_preset} is not in the catalog`);

  const dark = shadows(releaseCss, ".theme-cinema");
  const light = shadows(releaseCss, ".theme-workbench");
  const roleLines = (scheme) =>
    THEME_ROLES.map((role) => `  --theme-${scheme}-${role}: ${fallback[scheme][role]};`);

  const css = [
    "/* GENERATED FILE — do not edit. Run: npm run tokens:generate (scripts/generate-theme-tokens.mjs).",
    " *",
    ` * Source: beamfall-design-system token release ${release}`,
    " * (tokens/releases/<release>/beamfall-tokens.css), followed by the default preset's resolved",
    " * roles for both schemes. src/styles/theme.css maps the --theme-<scheme>-* values onto the",
    " * role names; the run-time theme seam (src/lib/theme/runtime.ts) overrides them inline.",
    " *",
    " * @import beamfall-tokens.css  ← governance marker (beamfall-design-system conformance)",
    " */",
    "",
    releaseCss.trimEnd(),
    "",
    `/* Resolved roles: preset "${fallback.id}" (default). */`,
    ":root {",
    ...roleLines("dark"),
    `  --theme-dark-shadow-card: ${dark.card};`,
    `  --theme-dark-shadow-window: ${dark.window};`,
    ...roleLines("light"),
    `  --theme-light-shadow-card: ${light.card};`,
    `  --theme-light-shadow-window: ${light.window};`,
    "}",
    "",
  ].join("\n");

  const palette = (hex, indent) =>
    ["{", ...THEME_ROLES.map((role) => `${indent}  "${role}": "${hex[role]}",`), `${indent}}`].join(
      "\n",
    );
  const ts = [
    "// GENERATED FILE — do not edit. Run: npm run tokens:generate (scripts/generate-theme-tokens.mjs).",
    `// Resolved from beamfall-design-system token release ${release} and its preset catalog.`,
    'import type { HexPalette, ThemeScheme } from "./resolver";',
    "",
    "export interface GeneratedThemePreset {",
    "  id: string;",
    "  nameKey: string;",
    "  dark: HexPalette;",
    "  light: HexPalette;",
    "}",
    "",
    `export const THEME_TOKEN_RELEASE = "${release}";`,
    "",
    `export const THEME_DEFAULT_PRESET = "${fallback.id}";`,
    "",
    "/** The default preset's dark accent: the brand accent core serves when no override is set. */",
    `export const THEME_DEFAULT_ACCENT = "${fallback.dark.accent}";`,
    "",
    "/** Completed base palettes (dark = Cinema, light = Workbench). */",
    "export const THEME_BASE_HEX: Record<ThemeScheme, HexPalette> = {",
    `  dark: ${palette(paletteToHex(base.dark), "  ")},`,
    `  light: ${palette(paletteToHex(base.light), "  ")},`,
    "};",
    "",
    "export const THEME_PRESETS: readonly GeneratedThemePreset[] = [",
    ...presets.map((p) =>
      [
        "  {",
        `    id: "${p.id}",`,
        `    nameKey: "${p.nameKey}",`,
        `    dark: ${palette(p.dark, "    ")},`,
        `    light: ${palette(p.light, "    ")},`,
        "  },",
      ].join("\n"),
    ),
    "];",
    "",
  ].join("\n");

  return { release, css, ts };
}

/** Returns the outputs whose committed bytes differ from a fresh generation. */
export function drift(dsDir = designSystemDir()) {
  const fresh = generate(dsDir);
  return Object.entries(OUTPUTS)
    .filter(([key, path]) => !existsSync(path) || readFileSync(path, "utf8") !== fresh[key])
    .map(([, path]) => path);
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const dsDir = designSystemDir();
  const unavailable = designSystemUnavailable(dsDir);
  if (unavailable) {
    console.error(`theme tokens: ${unavailable}`);
    console.error("set BEAMFALL_DESIGN_SYSTEM_DIR to a current beamfall-design-system checkout");
    process.exit(process.argv.includes("--check") ? 3 : 1);
  }
  if (process.argv.includes("--check")) {
    const stale = drift(dsDir);
    if (stale.length) {
      for (const path of stale) console.error(`DRIFT ${path}`);
      console.error("run: npm --prefix internal/web/app run tokens:generate");
      process.exit(1);
    }
    console.log(`theme tokens: generated files match design-system release (${dsDir})`);
  } else {
    const out = generate(dsDir);
    writeFileSync(OUTPUTS.css, out.css);
    writeFileSync(OUTPUTS.ts, out.ts);
    console.log(`theme tokens: wrote release ${out.release} outputs from ${dsDir}`);
  }
}
