// Design-system-backed checks (CORE-1179): the resolver against the golden vectors, and the
// generated token CSS and presets against a fresh generation. Both read the sibling
// beamfall-design-system checkout at test time ($BEAMFALL_DESIGN_SYSTEM_DIR, default
// ../beamfall-design-system next to the repository root); the vectors are never copied here.
// Without a usable checkout the tests are skipped with a warning, never passed.
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import {
  designSystemDir,
  designSystemUnavailable,
  drift,
} from "../../../scripts/generate-theme-tokens.mjs";
import { compileBasePalettes, paletteToHex, resolveTheme } from "./resolver";

const dsDir = designSystemDir();
const unavailable = designSystemUnavailable(dsDir);
const skipNote = unavailable ? ` — SKIPPED: ${unavailable}; set BEAMFALL_DESIGN_SYSTEM_DIR` : "";
if (unavailable) process.stderr.write(`\n[design-system checks${skipNote}]\n`);

interface Vector {
  id: string;
  layers: unknown[];
  options: Record<string, unknown>;
  expect: Record<string, unknown>;
}

function loadVectors() {
  const readJson = (path: string) => JSON.parse(readFileSync(join(dsDir, path), "utf8"));
  const release = readJson("tokens/releases/index.json").latestPublished as string;
  const tokens = readJson(`tokens/releases/${release}/beamfall-tokens.json`);
  const golden = readJson("tokens/theme/vectors.json") as { base: unknown; vectors: Vector[] };
  return { base: compileBasePalettes(tokens.theme), golden };
}

describe.skipIf(unavailable !== null)(
  `theme resolver golden vectors (design system)${skipNote}`,
  () => {
    const { base, golden } = unavailable
      ? { base: null, golden: { base: null, vectors: [] } }
      : loadVectors();

    it("compiles the token release into the vectors' base palettes", () => {
      if (!base) return;
      expect({ dark: paletteToHex(base.dark), light: paletteToHex(base.light) }).toEqual(
        golden.base,
      );
    });

    it("has vectors to check", () => {
      expect(golden.vectors.length).toBeGreaterThan(0);
    });

    for (const vector of golden.vectors) {
      it(vector.id, () => {
        if (!base) return;
        const result = JSON.parse(
          JSON.stringify(resolveTheme(vector.layers, vector.options, base)),
        ) as Record<string, unknown>;
        delete result.schemaVersion;
        expect(result).toEqual(vector.expect);
      });
    }
  },
);

describe.skipIf(unavailable !== null)(`generated theme tokens (design system)${skipNote}`, () => {
  it("match a fresh generation from the latest published release", () => {
    expect(drift(dsDir)).toEqual([]);
  });
});

describe("design-system checkout detection", () => {
  it("reports a missing checkout instead of failing or passing", () => {
    expect(designSystemUnavailable("/nonexistent/beamfall-design-system")).toMatch(
      /no beamfall-design-system checkout/,
    );
  });

  it("honours BEAMFALL_DESIGN_SYSTEM_DIR", () => {
    expect(designSystemDir({ BEAMFALL_DESIGN_SYSTEM_DIR: "/somewhere/ds" })).toBe("/somewhere/ds");
  });
});
