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
// setup wizard copy and CORE-1115 provider-key copy.
const BASE_SERIALIZED_LENGTH = 384_497;
const BASE_REGISTRY_SHA256 = "5487e589792ea91e09f31124dec2de94b0c44d15d54b0e566320e0349361a974";
const BASE_KEY_ORDER_SHA256 = "1f4b3e83af4adfc880ce22dd970f62dc87d8cff0b6e0c3c1bf7980af15a40f3b";
const BASE_PLACEHOLDER_SHA256 = "be38d4bec07e5630515205c7703582f936fbd5551b73834767d2c2ea7d51beb4";
const BASE_CATALOGS = {
  en: {
    partialCount: 1_459,
    partialSha256: "eb0f223b669865459f69648c1192d968a3ec4f13a9aba48f6e02161e5cfa3b81",
    resolvedSha256: "d337583f2f792be5126d8be7e977403e47ffa9444bb17b30565ee04ed920fe61",
  },
  fr: {
    partialCount: 658,
    partialSha256: "29492093b3c104e4b43bdb2b1d276612c6ad22cf3679815ad565ade83cf30429",
    resolvedSha256: "e3cfb1e4a661eb8f65f066e26bf677d662101f6ae6a47d573dd0ee7c1321d4b0",
  },
  de: {
    partialCount: 593,
    partialSha256: "a57acea5a0ffe58aecec534f90d46cab7d264439f6dba1febda1b1a2850f26a8",
    resolvedSha256: "4a57da515d13e5ca54fed89d817386b60ee8dd4c03a56fbce3d9241bd481c1b7",
  },
  es: {
    partialCount: 593,
    partialSha256: "b1caabcc756085d4d73d9f0d3f0b264561329481205063e7689a870ccc4fff40",
    resolvedSha256: "ef37fee1de40b362f8ee1505e24899d3ce63da2e2e87f2980a27d3426d38e62c",
  },
  pt: {
    partialCount: 593,
    partialSha256: "db3db6d17886fe7141944b1979c76600eb590226d0f216f2c5ea3271f050ad9e",
    resolvedSha256: "761d77535220b3c6cf32f1c7871129efd66060ab24ee7b04bf2965577bafe3bd",
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
