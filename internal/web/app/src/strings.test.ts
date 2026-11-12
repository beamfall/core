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
// setup wizard copy, CORE-1115 provider-key copy and CORE-1131 rescan and account-menu copy.
const BASE_SERIALIZED_LENGTH = 395_918;
const BASE_REGISTRY_SHA256 = "c18e11a2f2cf80818655e3f34838243ca3e2b014d554f258e07f4edf79835514";
const BASE_KEY_ORDER_SHA256 = "17b278d4ae3cefa10391c8b533213958c2282fc16dfd583b53d87b8ed39338a1";
const BASE_PLACEHOLDER_SHA256 = "35df51badb293b25e4c0eafbd1edb1dc7dd340053137f84c860738af28e5a2ab";
const BASE_CATALOGS = {
  en: {
    partialCount: 1_504,
    partialSha256: "c07bd8c4b195c79f3794d16bf5a404aeb5ad8c2d53e76883cc99431317449f3a",
    resolvedSha256: "ac7834616080e0d22e74c2b9768b1244a9c660fb9fc0e727d8e1e171d7aad857",
  },
  fr: {
    partialCount: 660,
    partialSha256: "5f224b439c544b556eeb775985b39de050d15479f5f5ef524964cec1316b1d6f",
    resolvedSha256: "f9332b8a18c5b38e41692929fce63ac8102fff75fa9d6d408689db90087694af",
  },
  de: {
    partialCount: 595,
    partialSha256: "7a146864b062f9780b572de136c2fd2f62d589038ef1144af161e0a0f2cf8665",
    resolvedSha256: "b074194da6a14f27c756205d1fabfd076dc3299a5d87d127d8c95edd29c0c12d",
  },
  es: {
    partialCount: 595,
    partialSha256: "4d7c1b2a6a7ff50aad8c739932e0fab761b603878df4899701b4ec9a32726eb5",
    resolvedSha256: "3f8792672bb9d50318da4952047d69019ba4373f5a396eac21bcac8894af52c2",
  },
  pt: {
    partialCount: 595,
    partialSha256: "8126c8df4f74c00eecc62d5194e91d499ef00a070ef0d7cd747cfba850b9f1f9",
    resolvedSha256: "356fce0cd0d81b24a7dd5373a103f08ef7179aeb6272e4b5f40cebf04bcc922d",
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
