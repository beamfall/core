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
// setup wizard copy, CORE-1115 provider-key copy, CORE-1131 rescan and account-menu copy
// and CORE-1142 folder-picker copy.
const BASE_SERIALIZED_LENGTH = 402_538;
const BASE_REGISTRY_SHA256 = "7708a600c7f96ea1bcce83133a691404b0354ebb8ee5a9e747fbd27cf93b1788";
const BASE_KEY_ORDER_SHA256 = "849acb871efe2241d6524338d980e454c3cc5b10d808da2ff745269356d80fe2";
const BASE_PLACEHOLDER_SHA256 = "02d3f1a80787d112d5d81885cb0b7071f284d10fbe029aca968bedbef3760402";
const BASE_CATALOGS = {
  en: {
    partialCount: 1_526,
    partialSha256: "9cd838b4ed84429733c2c524cacaaaff1946b00109dbff88b0f1651e49da94fc",
    resolvedSha256: "6f18fab24e93cea78f410c83fc14892a1ebf86902b7f5823ac05483c3b9c9268",
  },
  fr: {
    partialCount: 660,
    partialSha256: "5f224b439c544b556eeb775985b39de050d15479f5f5ef524964cec1316b1d6f",
    resolvedSha256: "8391453bd5a2c16b2ec834d3eaefe257a4d94ba264c85f9ad56b94694546a4ff",
  },
  de: {
    partialCount: 595,
    partialSha256: "7a146864b062f9780b572de136c2fd2f62d589038ef1144af161e0a0f2cf8665",
    resolvedSha256: "ec679619d0a37f3a4267973321d57cb5fd2f024442c325f311c6a37a185857c8",
  },
  es: {
    partialCount: 595,
    partialSha256: "4d7c1b2a6a7ff50aad8c739932e0fab761b603878df4899701b4ec9a32726eb5",
    resolvedSha256: "137b2944bcd12f5974cb6f88a6d5c20bc7df9ac752334f20feba8767fe1c7b5e",
  },
  pt: {
    partialCount: 595,
    partialSha256: "8126c8df4f74c00eecc62d5194e91d499ef00a070ef0d7cd747cfba850b9f1f9",
    resolvedSha256: "5668a53e33fef99ba8cba3a2f40c20b112894f6a7f64d4d66aa04ebb30c60bd9",
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
