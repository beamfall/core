import { createHash } from "node:crypto";
import { readdirSync, readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { type Locale, STRINGS, type StringKey, type StringTable } from "./strings";
import { de, en, es, fr, pt } from "./strings/catalog";

const LOCALES = ["en", "fr", "de", "es", "pt"] as const satisfies readonly Locale[];
const PARTIAL_CATALOGS: Record<Locale, Partial<StringTable>> = { en, fr, de, es, pt };

// Captured after WEB-HLS-RECOVERY-1 added localized playback-recovery keys atop
// WEBUI-SHELL-RESILIENCE-UPGRADE-BLANK-1 (2026-08-30); updated for CORE-1141 player settings copy.
const BASE_SERIALIZED_LENGTH = 376_524;
const BASE_REGISTRY_SHA256 = "fa8461137b008a3256934818050221e3e75004e712a64fbd0295601d2840371c";
const BASE_KEY_ORDER_SHA256 = "f1ff167542cceca0c7f81979db9f3ebd0cd7d44a6fb59856e7ea3bbcb4d6b0b0";
const BASE_PLACEHOLDER_SHA256 = "cfb037d0b30a58ca9303842d43e79294f3174e0d9efd08a5d21d1ff0a6510b55";
const BASE_CATALOGS = {
  en: {
    partialCount: 1_436,
    partialSha256: "3b6fbd5f7cd33346700ce9b865f415a31410d8b4c071c85f77274cc69dd9b129",
    resolvedSha256: "211d395b088b92038266b1d31b6de6153373ef0fff5b94caa96fe8a35e0a8aa4",
  },
  fr: {
    partialCount: 662,
    partialSha256: "66fdda7782af779cd68961b4604ad85f8a37a6bee8cb3d222ec15e9821d6e853",
    resolvedSha256: "86e27d3317664e33db76a67460cfe61f47fd874ae67821489a89cdb82c47be1e",
  },
  de: {
    partialCount: 597,
    partialSha256: "a2f40b8b383e8a36330fc80b10baa90908fd4d249069a234306ce686d4d7d5b4",
    resolvedSha256: "98377cf56a75363558cc00c8ee20fc5083cca37f6379bfc7167d46f998e5c657",
  },
  es: {
    partialCount: 597,
    partialSha256: "4ccb68dc155c37fdf923e38dd32b14dde745889d5a9c0768adf569644be68e4d",
    resolvedSha256: "1bf987a52411085ec5000bdc339d1b167d6348e1e535bf8480d474ac30b9fb7a",
  },
  pt: {
    partialCount: 597,
    partialSha256: "3514e1afe77e6bc886215f6a2477c0b308c673fa20f4443709571b6ac3f348d4",
    resolvedSha256: "53269689af28b23cb425ef639cf55afa5e48bc1ebfdd601061d6aaa82d58b206",
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
