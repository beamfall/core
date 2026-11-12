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
const BASE_SERIALIZED_LENGTH = 392_392;
const BASE_REGISTRY_SHA256 = "d17669ac2d3cf8e611c06f1a96cb1abf9f1c7d87b700b6313cbdd0623fd8a5c7";
const BASE_KEY_ORDER_SHA256 = "0328bc1b3fb26ca792e4da561086dc143ac67e75f777bd889d87d7ad0e57cfa4";
const BASE_PLACEHOLDER_SHA256 = "0bc99e58725c0a5176bda95c890030aaa9c3accd1d0c38c34e2f68d131632a76";
const BASE_CATALOGS = {
  en: {
    partialCount: 1_489,
    partialSha256: "e843292b0a84a6ffcd533a6cf741ad3af834025cd2e956f8e697a0b41604c95e",
    resolvedSha256: "42ad191be71a8d1076b88c406a6cb53b46383c1811b169eabce60a0d103d3534",
  },
  fr: {
    partialCount: 658,
    partialSha256: "29492093b3c104e4b43bdb2b1d276612c6ad22cf3679815ad565ade83cf30429",
    resolvedSha256: "9ac9b480c30c4c56da2e025391dcf57fbd3a204ba0b55cf544d973d9052fab6f",
  },
  de: {
    partialCount: 593,
    partialSha256: "a57acea5a0ffe58aecec534f90d46cab7d264439f6dba1febda1b1a2850f26a8",
    resolvedSha256: "a49bef6cd38c663e63b631e3f3a32b14a3bb7ada861e04f67d00796b1e53387e",
  },
  es: {
    partialCount: 593,
    partialSha256: "b1caabcc756085d4d73d9f0d3f0b264561329481205063e7689a870ccc4fff40",
    resolvedSha256: "0a38be19a87b83e337c8a8cb82bf1232534bd822a5b1fc04e462fcddccc3965b",
  },
  pt: {
    partialCount: 593,
    partialSha256: "db3db6d17886fe7141944b1979c76600eb590226d0f216f2c5ea3271f050ad9e",
    resolvedSha256: "3fd77acd65ec13185640a67715963bdf1c0e0c6f8eaf133ef96d0a0e3bb5e98e",
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
