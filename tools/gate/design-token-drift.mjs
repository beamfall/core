#!/usr/bin/env node
import { existsSync, readFileSync } from "node:fs";
import { resolve } from "node:path";

const corePath = resolve("internal/web/app/src/styles/tokens.css");
const dsPath = resolve("../beamfall-design-system/tokens/generated/beamfall-tokens.css");

if (!existsSync(dsPath)) {
  console.log("design-token-drift: skipped (../beamfall-design-system not present)");
  process.exit(0);
}

const core = readFileSync(corePath, "utf8");
const ds = readFileSync(dsPath, "utf8");

function vars(css, selector) {
  const out = {};
  const re = new RegExp(`${selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}\\s*\\{([\\s\\S]*?)\\}`, "g");
  for (const match of css.matchAll(re)) {
    for (const m of match[1].matchAll(/--([a-z0-9-]+):\s*([^;]+);/gi)) {
      out[m[1]] = normalize(m[2]);
    }
  }
  if (!Object.keys(out).length) throw new Error(`missing selector ${selector}`);
  return out;
}

function normalize(value) {
  return value.trim().toLowerCase().replaceAll("0.", ".").replace(/\s+/g, " ");
}

function resolveVar(map, name, seen = new Set()) {
  const value = map[name];
  if (!value || seen.has(name)) return value;
  const match = value.match(/^var\(--([a-z0-9-]+)\)$/);
  if (!match) return value;
  seen.add(name);
  return resolveVar(map, match[1], seen) || value;
}

const checks = [
  [":root", ":root", ["night-950", "night-900", "night-800", "paper-50", "paper-100", "paper-200", "beam-500", "green-500", "cyan-500", "iris-500", "amber-500", "red-500"]],
  [":root", ".theme-cinema", ["bg-root", "bg-surface", "bg-raised", "line", "text", "text-muted", "accent", "accent-ink", "state-direct", "state-remote", "state-restricted", "state-warning", "state-danger", "focus-ring"]],
  ['[data-register="manage"]', ".theme-workbench.dark", ["bg-root", "bg-surface", "bg-raised", "line", "text", "text-muted", "accent", "accent-ink", "state-direct", "state-remote", "state-restricted", "state-warning", "state-danger", "focus-ring"]],
  ['[data-register="manage"][data-theme="light"]', ".theme-workbench", ["bg-root", "bg-surface", "bg-raised", "line", "text", "text-muted", "accent", "accent-ink", "accent-on-light", "state-direct", "state-remote", "state-restricted", "state-warning", "state-danger", "focus-ring"]],
];

const coreRoot = vars(core, ":root");
const dsRoot = vars(ds, ":root");
let drift = false;
for (const [coreSel, dsSel, names] of checks) {
  const coreVars = { ...coreRoot, ...vars(core, coreSel) };
  const dsVars = { ...dsRoot, ...vars(ds, dsSel) };
  for (const name of names) {
    const got = coreVars[name];
    const want = dsVars[name];
    const gotResolved = resolveVar(coreVars, name);
    const wantResolved = resolveVar(dsVars, name);
    if (!gotResolved || !wantResolved || gotResolved !== wantResolved) {
      drift = true;
      console.error(`DRIFT ${coreSel} --${name}: got ${got || "missing"}, want ${want || "missing"} (${dsSel})`);
    }
  }
}

if (drift) process.exit(1);
console.log("design-token-drift: core tokens match generated design-system tokens");
