import { createHash } from "node:crypto";
import { readdirSync, readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { type Locale, STRINGS, type StringKey, type StringTable } from "./strings";
import { de, en, es, fr, pt } from "./strings/catalog";

const LOCALES = ["en", "fr", "de", "es", "pt"] as const satisfies readonly Locale[];
const PARTIAL_CATALOGS: Record<Locale, Partial<StringTable>> = { en, fr, de, es, pt };

// Captured after WEB-HLS-RECOVERY-1 added localized playback-recovery keys atop
// WEBUI-SHELL-RESILIENCE-UPGRADE-BLANK-1 (2026-08-30); updated for CORE-1141 player settings copy
// and the CORE-1197 Manage unlock copy.
const BASE_SERIALIZED_LENGTH = 377_578;
const BASE_REGISTRY_SHA256 = "036c27500d602909306982fa297e3b5b2c4fa1578aaacf50891c220234b65011";
const BASE_KEY_ORDER_SHA256 = "0f7b009b0eb52a128c228c4b5895ffd3c9396ffcb15aac12fc93b6180ce2587f";
const BASE_PLACEHOLDER_SHA256 = "cfb037d0b30a58ca9303842d43e79294f3174e0d9efd08a5d21d1ff0a6510b55";
const BASE_CATALOGS = {
  en: {
    partialCount: 1_439,
    partialSha256: "63c9cbdc465c23dfe81d132367a7581a2efe3d4e9035b71405aa8be15decc338",
    resolvedSha256: "fd298cf5e6ddbc1b286e520eed68d49e956fff304737cf3d3eedfceab84e38ef",
  },
  fr: {
    partialCount: 665,
    partialSha256: "bb7ef2c8200f08ed48bc87db0f2d99d5c529ec8381fb97b4855fa31a49f2c4b7",
    resolvedSha256: "bc6259ded3082ec29578bc0c8671c80ac0e4e96c1494dfc8ad68efeaa9adf101",
  },
  de: {
    partialCount: 600,
    partialSha256: "a068b2a6cc8bdaa411d32694e695619dc3a5bb4fea5aa70bda23c663ffe1441c",
    resolvedSha256: "5de7a8d2173772412b613900c0f73766baccc5edf8949d6e766b799b49356e7d",
  },
  es: {
    partialCount: 600,
    partialSha256: "16ade231d6beb9e07948f345421aa25c9d2965f16caa92b989707dcebf9d13c6",
    resolvedSha256: "a2becf378ca2e9891a3126fb4613d5b04443ea4ee7157b70b64b7e3c9ce04b2a",
  },
  pt: {
    partialCount: 600,
    partialSha256: "8cb7ac3141e2856babb2256adab6ec3e5f1147cc86cc9da356cac19649f4ce89",
    resolvedSha256: "058cb4e9d14d7888c1e3dbfac939d8dd0cd18eaa5083a3006427b34d39b6a516",
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
