import { createHash } from "node:crypto";
import { readdirSync, readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { type Locale, STRINGS, type StringKey, type StringTable } from "./strings";
import { de, en, es, fr, pt } from "./strings/catalog";

const LOCALES = ["en", "fr", "de", "es", "pt"] as const satisfies readonly Locale[];
const PARTIAL_CATALOGS: Record<Locale, Partial<StringTable>> = { en, fr, de, es, pt };

// Captured after WEB-HLS-RECOVERY-1 added localized playback-recovery keys atop
// WEBUI-SHELL-RESILIENCE-UPGRADE-BLANK-1 (2026-08-30); updated for CORE-1141 player settings copy,
// the CORE-1197 Manage unlock copy and the CORE-1143 audio player and queue copy.
const BASE_SERIALIZED_LENGTH = 379_264;
const BASE_REGISTRY_SHA256 = "d1e7ca17d4b715dd29f70a2f872e45a039d12c23fc9bf4e834a0190ca782ca1b";
const BASE_KEY_ORDER_SHA256 = "5cf8e61fc75c23c0b1f2742d0ace4604b56b944dddccde62fb7300177c659900";
const BASE_PLACEHOLDER_SHA256 = "3481664980f7bc21739f91afb5aba0074dd976ae2d1d5b397a9eb06b52f58fce";
const BASE_CATALOGS = {
  en: {
    partialCount: 1_444,
    partialSha256: "dc66b711074f0846b7ca5874441bff2f1e963a04922cf27b3a8b1036b9de87f4",
    resolvedSha256: "d6420db8ea68261f80cde1826eb98c12a2090153ba7fe5793d28a08a98c26252",
  },
  fr: {
    partialCount: 671,
    partialSha256: "0fa78287a6b54ae4cb2a7715ac25f77dadeba85ae3fd4cbadbd0dd17a975ba1b",
    resolvedSha256: "820f16858d3075e19cb72c06e8e5902e90b2371a2815bbe4fb25275cccb9b40a",
  },
  de: {
    partialCount: 606,
    partialSha256: "10c48f7cda423a81e1760069b82f8e65dfa678b10f5179723c40833a7f308132",
    resolvedSha256: "625ba9060eb7e5d5dd652cec7fcae490f7eb88dd42d301e333013aec30a11d32",
  },
  es: {
    partialCount: 606,
    partialSha256: "ca5b108b9da366c663f85bd8d1570ce61da20e90a82e64577318ff6c8ce08655",
    resolvedSha256: "9749997b7422cb54d7b493a33b6a9668712fc4e93d98e9aa109497687a704eda",
  },
  pt: {
    partialCount: 606,
    partialSha256: "481bb74b60defc81e983ab060c77d125a49f84f99d168d012a5b96f940db9115",
    resolvedSha256: "cb0140feabf65efa725785849c1da320770f0601253edecc1c95efff81b92b9c",
  },
} as const satisfies Record<
  Locale,
  { partialCount: number; partialSha256: string; resolvedSha256: string }
>;

function sha256(serialized: string): string {
  return createHash("sha256").update(serialized).digest("hex");
}

function sortedSerialization(table: Partial<StringTable>): string {
  const keys = (Object.keys(table) as StringKey[]).sort();
  return JSON.stringify(Object.fromEntries(keys.map((key) => [key, table[key]])));
}

function withEnglishFallback(partial: Partial<StringTable>): StringTable {
  const keys = Object.keys(en) as StringKey[];
  return Object.fromEntries(keys.map((key) => [key, partial[key] ?? en[key]])) as StringTable;
}

function placeholderInventory(table: StringTable): Record<string, string[]> {
  const inventory: Record<string, string[]> = {};
  for (const [key, value] of Object.entries(table)) {
    const placeholders = [...value.matchAll(/\{([^{}]+)\}/g)].map((match) => match[1]);
    if (placeholders.length > 0) inventory[key] = placeholders;
  }
  return inventory;
}

describe("localized string assembly", () => {
  it("matches the pre-split JSON serialization exactly", () => {
    const serialized = JSON.stringify(STRINGS);

    expect(serialized).toHaveLength(BASE_SERIALIZED_LENGTH);
    expect(sha256(serialized)).toBe(BASE_REGISTRY_SHA256);
  });

  it("preserves locale membership, key insertion order, and catalog values", () => {
    const englishKeys = Object.keys(en) as StringKey[];

    expect(Object.keys(STRINGS)).toEqual(LOCALES);
    expect(sha256(JSON.stringify(englishKeys))).toBe(BASE_KEY_ORDER_SHA256);

    for (const locale of LOCALES) {
      const baseline = BASE_CATALOGS[locale];
      const partial = PARTIAL_CATALOGS[locale];

      expect(Object.keys(partial)).toHaveLength(baseline.partialCount);
      expect(sha256(sortedSerialization(partial))).toBe(baseline.partialSha256);
      expect(Object.keys(STRINGS[locale])).toEqual(englishKeys);
      expect(sha256(JSON.stringify(STRINGS[locale]))).toBe(baseline.resolvedSha256);
    }
  });

  it("keeps English complete and resolves every missing locale key through English", () => {
    const englishKeys = Object.keys(en) as StringKey[];

    expect(Object.values(en).every((value) => typeof value === "string" && value.length > 0)).toBe(
      true,
    );
    for (const locale of LOCALES) {
      const partial = PARTIAL_CATALOGS[locale];
      expect(STRINGS[locale]).toEqual(withEnglishFallback(partial));

      if (locale === "en") continue;
      const missingKeys = englishKeys.filter((key) => partial[key] === undefined);
      expect(missingKeys.length).toBeGreaterThan(0);
      for (const key of missingKeys) expect(STRINGS[locale][key]).toBe(en[key]);
    }
  });

  it("preserves placeholder names and order in every resolved catalog", () => {
    const englishInventory = placeholderInventory(STRINGS.en);

    expect(sha256(JSON.stringify(englishInventory))).toBe(BASE_PLACEHOLDER_SHA256);
    for (const locale of LOCALES) {
      expect(placeholderInventory(STRINGS[locale])).toEqual(englishInventory);
    }
  });
});

describe("localized string source limits", () => {
  const sourceDirectory = resolve(process.cwd(), "src");
  const shardDirectory = resolve(sourceDirectory, "strings");
  const sourceFiles = [
    resolve(sourceDirectory, "strings.ts"),
    resolve(sourceDirectory, "strings.test.ts"),
    ...readdirSync(shardDirectory, { withFileTypes: true })
      .filter((entry) => entry.isFile() && entry.name.endsWith(".ts"))
      .map((entry) => resolve(shardDirectory, entry.name)),
  ];

  it("keeps every authored registry source within 20 KB and 600 lines", () => {
    for (const file of sourceFiles) {
      const source = readFileSync(file, "utf8");

      expect(Buffer.byteLength(source), `${file} byte size`).toBeLessThanOrEqual(20_000);
      expect(source.split("\n").length, `${file} line count`).toBeLessThanOrEqual(600);
    }
  });
});
