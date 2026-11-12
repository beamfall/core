import { createHash } from "node:crypto";
import { readdirSync, readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { type Locale, STRINGS, type StringKey, type StringTable } from "./strings";
import { de, en, es, fr, pt } from "./strings/catalog";

const LOCALES = ["en", "fr", "de", "es", "pt"] as const satisfies readonly Locale[];
const PARTIAL_CATALOGS: Record<Locale, Partial<StringTable>> = { en, fr, de, es, pt };

// Captured after WEB-HLS-RECOVERY-1 added localized playback-recovery keys atop
// WEBUI-SHELL-RESILIENCE-UPGRADE-BLANK-1 (2026-08-30); updated for CORE-1106
// admin-password sign-in keys, CORE-1110 sign-in security keys, CORE-1114
// setup wizard copy, CORE-1115 provider-key copy and CORE-1131 rescan copy.
const BASE_SERIALIZED_LENGTH = 390_937;
const BASE_REGISTRY_SHA256 = "de392dcab1849ba03cfd73396bd518c11c17f9e73ea3f33d03cdf496112653c8";
const BASE_KEY_ORDER_SHA256 = "d6f5134cdaf39247c755a43bb1ddc87eee99c6e08075ecd32f52fc9e675400e6";
const BASE_PLACEHOLDER_SHA256 = "dffe2a3d033eab28ac8b2980afc54360942dbd25361232f2f9fc356aaf2e0cbf";
const BASE_CATALOGS = {
  en: {
    partialCount: 1_484,
    partialSha256: "1f8dfacb21c59c7af9fe9efbe6fc1e22baa63492d3ff54715e48cefeeb26c884",
    resolvedSha256: "2fc2a1526b46d674eb9950dcdfb01cf6fe5ef105ea13e69abc95b42f9f6cffb0",
  },
  fr: {
    partialCount: 658,
    partialSha256: "29492093b3c104e4b43bdb2b1d276612c6ad22cf3679815ad565ade83cf30429",
    resolvedSha256: "e38f74d56a70e93b633a37ec4fe6ac231a5e919a70cc07e9fb58eb397bad0c5d",
  },
  de: {
    partialCount: 593,
    partialSha256: "a57acea5a0ffe58aecec534f90d46cab7d264439f6dba1febda1b1a2850f26a8",
    resolvedSha256: "d336ba60e9c145e9391b4f4caade417f19b2e96f22091d6c25dbd5ff4e68ebe9",
  },
  es: {
    partialCount: 593,
    partialSha256: "b1caabcc756085d4d73d9f0d3f0b264561329481205063e7689a870ccc4fff40",
    resolvedSha256: "5341a7113a4cddf1533f55600273cc72ac597c33167d80c4ddd12e41a80e77b7",
  },
  pt: {
    partialCount: 593,
    partialSha256: "db3db6d17886fe7141944b1979c76600eb590226d0f216f2c5ea3271f050ad9e",
    resolvedSha256: "89b8bfecdcf6ec5cf76dc42822ac3b8c45ba9a7d271fa316eff4cea3a0ef300e",
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
