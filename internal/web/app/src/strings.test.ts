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
const BASE_SERIALIZED_LENGTH = 386_012;
const BASE_REGISTRY_SHA256 = "47733584f69a7bee0ecf182afd2d46b2af4607406755af0d377c55da9950e2e5";
const BASE_KEY_ORDER_SHA256 = "c86587e63d3a2a6573451e1b532825a73d41411799c685adcd0cdde25c3726fe";
const BASE_PLACEHOLDER_SHA256 = "be38d4bec07e5630515205c7703582f936fbd5551b73834767d2c2ea7d51beb4";
const BASE_CATALOGS = {
  en: {
    partialCount: 1_467,
    partialSha256: "c825dbe8589c4d1065feaacdc8920b3ad1bf3d834fc62b0f92a1e90d3093a802",
    resolvedSha256: "a2d8c1a5bfa9dd3fc3af8601964a0217f8052c640c7547dda7e6776bbe6d3aab",
  },
  fr: {
    partialCount: 658,
    partialSha256: "29492093b3c104e4b43bdb2b1d276612c6ad22cf3679815ad565ade83cf30429",
    resolvedSha256: "154961f27b64016aaf93371f61b3d7bfec0a167232f57586ec3bfb1f109f17ee",
  },
  de: {
    partialCount: 593,
    partialSha256: "a57acea5a0ffe58aecec534f90d46cab7d264439f6dba1febda1b1a2850f26a8",
    resolvedSha256: "46b3f4935f9e150f5d483ee32d7597a1b81f8bd4e1f6ffd65fba7d33cd750eb6",
  },
  es: {
    partialCount: 593,
    partialSha256: "b1caabcc756085d4d73d9f0d3f0b264561329481205063e7689a870ccc4fff40",
    resolvedSha256: "60489f3d902b7c7b1110e918706141265d3870de9fad82b601f401904558d5ea",
  },
  pt: {
    partialCount: 593,
    partialSha256: "db3db6d17886fe7141944b1979c76600eb590226d0f216f2c5ea3271f050ad9e",
    resolvedSha256: "fdedd5bbd14bab543f0f9b423a35f9350125469d72751b45067ead83eb17192c",
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
