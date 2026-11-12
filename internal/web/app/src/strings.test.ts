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
// the CORE-1197 Manage unlock copy, the CORE-1190 library browse copy, the CORE-1143 audio
// player and queue copy and the CORE-1194 item action copy.
const BASE_SERIALIZED_LENGTH = 388_646;
const BASE_REGISTRY_SHA256 = "b75c45b6d9722ea601afbffb2b76ccde810510038ba06e8c1aadada32d71faaf";
const BASE_KEY_ORDER_SHA256 = "69d63ca8a6fb1211dcbc7995b3b02fa3f347abfce55229c539b5b4b018296f03";
const BASE_PLACEHOLDER_SHA256 = "c4255dd5e059360fe913df1cd9822c6bd2f2ed674ca05657e50e4149c66cd331";
const BASE_CATALOGS = {
  en: {
    partialCount: 1_490,
    partialSha256: "b0e31d8336e287e04bf97ace1d538930f7a5b0440f1a3b27a7f628b4d2f0be24",
    resolvedSha256: "c3fea17e9ec96b02eb888613f2f50d519ad0df511dc29a58cd0adb77ea77723f",
  },
  fr: {
    partialCount: 713,
    partialSha256: "d457747733bbc452a92fa88fa886cc892a53d37fbea2e0771f2e65c12d0c2134",
    resolvedSha256: "4ae850907ae259b342656f6f2017be90d593fdcd5420838fb3158b63d3d39cc8",
  },
  de: {
    partialCount: 614,
    partialSha256: "401672b8b71885c6b6695d6ce3d7856b095aec4f72b87aff1145333a82ec40ed",
    resolvedSha256: "4196271c13cdd5ca76e7b8c8d19715612f4f3aab2d83e0e6e61a787c26116f4e",
  },
  es: {
    partialCount: 614,
    partialSha256: "537d6bd60c887986dfbcc0b7d6dfd6213e82a5130217c47fbdc40efe9b5720cb",
    resolvedSha256: "f97c157636438a15ae12a63e973ae42a99f2cb1ce828943d0dffe4d903ac1652",
  },
  pt: {
    partialCount: 614,
    partialSha256: "d693c9b1b977d34e90f19a45261f96d81ec40f2f57c792b3821f39ad580aa41a",
    resolvedSha256: "d97a901fc4b28ef887bb03f39880a267a61ccc1e53eabef53ab1b44893609e54",
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
    // Argument names only: an ICU plural contributes its argument, not its translated branches.
    const placeholders = [...value.matchAll(/\{\s*([A-Za-z_]\w*)\s*[,}]/g)].map(
      (match) => match[1],
    );
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
