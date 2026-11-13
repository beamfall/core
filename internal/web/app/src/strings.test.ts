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
// player and queue copy, the CORE-1194 item action copy, the CORE-1192 card state copy, the
// CORE-1195 console dashboard copy (with its memory-unavailable fix), the CORE-1191 header search copy and the CORE-1196
// who's-watching copy; merged with CORE-1106 admin-password sign-in keys, CORE-1110 sign-in
// security keys, CORE-1114 setup wizard copy, CORE-1115 provider-key copy, CORE-1131 rescan and
// account-menu copy and CORE-1142 folder-picker copy; updated for the CORE-1146, CORE-1362 and
// CORE-1363 player recovery copy, the CORE-1342 source-ends-early copy and the CORE-0393
// Viewer pairing-code switch copy.
const BASE_SERIALIZED_LENGTH = 448_456;
const BASE_REGISTRY_SHA256 = "6ba5a916c2a7ad91917e4a591cd7456451f5ea9ef37394465d3b1cc5ad102490";
const BASE_KEY_ORDER_SHA256 = "a1162c7312cda34806940f1470061d4a0c2ca625d30ae1bf930f81c1f42841b2";
const BASE_PLACEHOLDER_SHA256 = "782ff5a1cd98e210086c59f4ea572a3e3410c26f71d08dad87b5f8c2f7a90c91";
const BASE_CATALOGS = {
  en: {
    partialCount: 1_715,
    partialSha256: "a89e9a297eaf76625525e62d4366b8c5efcaf7a79a6536874d56e6aeb0e901fe",
    resolvedSha256: "28f25b2a33c91bdd52e83db3d74d3f0d46c9b8548dbd2adf7a4782389ea24c00",
  },
  fr: {
    partialCount: 791,
    partialSha256: "f9a01315c2dc1d6b908f1bc008e2a330a8b302d22a16a43ad2ce118dedabd9d5",
    resolvedSha256: "f318c72583d53b9d17c6eb32ecd61fc2d6b1bbb47c71763cd794575429fb00da",
  },
  de: {
    partialCount: 692,
    partialSha256: "a9dba218377688fedb2ae78e68b37a3d8392abebd6cf9576f40fd5f5161bcb0d",
    resolvedSha256: "94f8ca2ddc74ecb5f0a0473791549b46fabb55959220bdc7a8f324a9c4b5740c",
  },
  es: {
    partialCount: 692,
    partialSha256: "7bcead143a953ed2582920d38829a117f6b87383081e2481a3cf7342e0e7edb5",
    resolvedSha256: "67bb013d2512e4ce72828c61128a1766ae822ce9e595064269f20db561a1bedb",
  },
  pt: {
    partialCount: 692,
    partialSha256: "8ca798169a311cd1c8a07729cdbc4a8c3f61769d7c58ae185f8df27d6d0f2fef",
    resolvedSha256: "602245fa3f8ef474a4a5aa7474dc2d275757a269b3ea6be25a178a99ca646116",
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
