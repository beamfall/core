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
// admin-password sign-in keys, CORE-1110 sign-in security keys and CORE-1114
// setup wizard copy.
const BASE_SERIALIZED_LENGTH = 381_564;
const BASE_REGISTRY_SHA256 = "a64d9c32e552446f638e8f5958d88b92de73964693798ea6cb5cf1aaa64232a1";
const BASE_KEY_ORDER_SHA256 = "a13fab1cb3c4300819aa87ff9472052594c1e77bbf3a7afcca15f5bb991ffe4d";
const BASE_PLACEHOLDER_SHA256 = "4f9dc14a113a91f1ee7ce9fc5f84feec94afd722f6f64c2913a63f190e2f9011";
const BASE_CATALOGS = {
  en: {
    partialCount: 1_454,
    partialSha256: "368fe430d1b3b428861ab895dce96e51c2a2890fa9525c657e417e729cef8d74",
    resolvedSha256: "a2367206ae47ba446b0ad0ce7ce01fb2ade2b379524ba2d2c96db55e51a78838",
  },
  fr: {
    partialCount: 666,
    partialSha256: "13f4a1829936cdb2bf66b32a3579ac87ddbdcb1d226be975d2e1675a574cb0d5",
    resolvedSha256: "8e0cc7d103587543c8855d942b95482a88d81a1ade6d2659b0171a4c55122a50",
  },
  de: {
    partialCount: 601,
    partialSha256: "285975078edeee2c5383d782e84662cabf165a854b6a5bd55c65f249831ab3df",
    resolvedSha256: "d02c8fb8437d53db6aab8b067e661809bbc19ccd6b4454e27b433d6fbc779335",
  },
  es: {
    partialCount: 601,
    partialSha256: "2647242c968bad5a26ef361097f643b0f3bf26ea0c92298185ff70890c5cd5df",
    resolvedSha256: "da08124f1cdc43ca56d8310652a99a0d85c218a803344dbda0932fa691284f5b",
  },
  pt: {
    partialCount: 601,
    partialSha256: "824d17d2f121e21962c704730f73eacf2420034b31478f3f948776d1b809e698",
    resolvedSha256: "53a9692348da99ba334f1cd210ea3411e93221c79aac6fc3bbd4a09919c26ed3",
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
