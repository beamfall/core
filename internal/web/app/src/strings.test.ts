import { createHash } from "node:crypto";
import { readdirSync, readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { type Locale, STRINGS, type StringKey, type StringTable } from "./strings";
import { de, en, es, fr, pt } from "./strings/catalog";

const LOCALES = ["en", "fr", "de", "es", "pt"] as const satisfies readonly Locale[];
const PARTIAL_CATALOGS: Record<Locale, Partial<StringTable>> = { en, fr, de, es, pt };

// Captured after WEB-HLS-RECOVERY-1 added localized playback-recovery keys atop
// WEBUI-SHELL-RESILIENCE-UPGRADE-BLANK-1 (2026-08-30).
const BASE_SERIALIZED_LENGTH = 376_365;
const BASE_REGISTRY_SHA256 = "1c8b13aeb08896685f0bec4c568c8c9db2916d63c9aa284f66b4d8a506335e4f";
const BASE_KEY_ORDER_SHA256 = "4f37a22eaa9d9f4c65da32360c9fee3fc0ff5baa084a48f7712be51a7d0e2bc1";
const BASE_PLACEHOLDER_SHA256 = "cfb037d0b30a58ca9303842d43e79294f3174e0d9efd08a5d21d1ff0a6510b55";
const BASE_CATALOGS = {
  en: {
    partialCount: 1_435,
    partialSha256: "dd9f601b90e34c7eb10719337e925d958310e0ce18d46a35a0fb755f09ea983b",
    resolvedSha256: "1f134282c0634d4a0b6b9a2d7c2125ba913a1bb4941667d636872caa290fe78b",
  },
  fr: {
    partialCount: 661,
    partialSha256: "7ce39672d0c8a8d2b326f9e8cba96b327096cfcfe71b77bbf693a23153fcec97",
    resolvedSha256: "7da7a6a013c582dbc6c61aaf30e02f3ddc7c0f25694ade6f5895f0373fb2fc0d",
  },
  de: {
    partialCount: 596,
    partialSha256: "e2731c519d5d6ff745b4dac5b2e07a0ac93d6fef0b14f93c1395aa9edd49ecac",
    resolvedSha256: "62d2c477acd88b6fd35e58032703186360f04e3a7b7d7bf2819321c21aabdbd0",
  },
  es: {
    partialCount: 596,
    partialSha256: "23a81991a532dff344eba7a91bc4d90931c19836475ae855293249049b670172",
    resolvedSha256: "dfe680cbc0640b0d0958d0dcc3d927374a01a1ade063b1b4578a1ce3382888bf",
  },
  pt: {
    partialCount: 596,
    partialSha256: "e5dc871adbd336143addf2982ef9e558b97eff051672dd5a64b9dcf8c92d0176",
    resolvedSha256: "b941eeb21f96b9983c0024f5f90f471c990ff513ae287054732198037c45ddda",
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
