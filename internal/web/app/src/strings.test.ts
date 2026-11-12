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
const BASE_SERIALIZED_LENGTH = 395_542;
const BASE_REGISTRY_SHA256 = "c782211c7951bbb1f0f4dd809f7b1e5ecbcd090b38457ddbe1426ff873e0745b";
const BASE_KEY_ORDER_SHA256 = "bfcfe224692ff5de0c6af2305c0001762f5c08d24d87b61c32ad190ff357ddb8";
const BASE_PLACEHOLDER_SHA256 = "6f7b3050ca2d93829b3d8584b985c80a3ab5f5202fa57ddea43fa213c1689743";
const BASE_CATALOGS = {
  en: {
    partialCount: 1_502,
    partialSha256: "0a9718c159520e20d9e1cfa7a74ce6e32b8cadecb687bb18690bcf0e680ae17b",
    resolvedSha256: "431f63673055ad5bde76910b1257d1ec02acb7ed75de803fee41d651f351789c",
  },
  fr: {
    partialCount: 658,
    partialSha256: "29492093b3c104e4b43bdb2b1d276612c6ad22cf3679815ad565ade83cf30429",
    resolvedSha256: "13aea5960700cdfb293761a37ba8093ddb33ea1d49229a1dad04de3fee8dd556",
  },
  de: {
    partialCount: 593,
    partialSha256: "a57acea5a0ffe58aecec534f90d46cab7d264439f6dba1febda1b1a2850f26a8",
    resolvedSha256: "8829dac78799dceb79a7fb85dd70335230882277ed48f00a4cef2e2ad4abc45b",
  },
  es: {
    partialCount: 593,
    partialSha256: "b1caabcc756085d4d73d9f0d3f0b264561329481205063e7689a870ccc4fff40",
    resolvedSha256: "0d04275ae39af1b6e68c09247cd96a725645a68a7675cc7cad8721d9ff596dcb",
  },
  pt: {
    partialCount: 593,
    partialSha256: "db3db6d17886fe7141944b1979c76600eb590226d0f216f2c5ea3271f050ad9e",
    resolvedSha256: "d209c71508a307e3ea2f4d842482646ac1de6381c859d7bd00d7f6377c5b71bd",
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
