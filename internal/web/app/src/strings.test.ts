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
// CORE-1363 player recovery copy, the CORE-1342 source-ends-early copy and the CORE-1132
// first admin password copy.
const BASE_SERIALIZED_LENGTH = 449_566;
const BASE_REGISTRY_SHA256 = "6cef6f2c64de9446714e17c569eb4723a8dc3dbc9fa41e63ff122055f02bb311";
const BASE_KEY_ORDER_SHA256 = "2c94fe79b0ffd2d491a7cf7dfb58929d95e6cc35a501fa379d535ef3d8fad84f";
const BASE_PLACEHOLDER_SHA256 = "782ff5a1cd98e210086c59f4ea572a3e3410c26f71d08dad87b5f8c2f7a90c91";
const BASE_CATALOGS = {
  en: {
    partialCount: 1_718,
    partialSha256: "abef60c951b328b06ea25804a3d09dc953469e253f279d91de010c5c22f383c6",
    resolvedSha256: "244647c9e7874e7a7285f52cbf6e791f6a44a2c845dfcf930665fb28ab111df3",
  },
  fr: {
    partialCount: 794,
    partialSha256: "d98db88323e59414b2b36d329ab341bacb648b3e8bcb34b644e0c52710a8ed2b",
    resolvedSha256: "1d26dbe98a05541d9f1d2b662f464a9f39d0eb13fa6e10c19705f2965e7053ee",
  },
  de: {
    partialCount: 690,
    partialSha256: "0aeeac83668cc034d43e0b673763e838d0bc6532161238562da6888339e7228c",
    resolvedSha256: "255909e4c290acaa28cb3bc3a7ee14b1b2d0e7b74a6b31986ac8b5ea4980e92e",
  },
  es: {
    partialCount: 690,
    partialSha256: "16de75c92d238cc09a5b489555fe326fd2911a7de51876ab3d65d89caa5dc8d5",
    resolvedSha256: "f532fb57b1dacebba82459f56ccb366dffe4e357b46344ca99f33356f0144f28",
  },
  pt: {
    partialCount: 690,
    partialSha256: "4a83e437501287796f76ae219040ca8af7e65da862d2f55a0f7970f9b62a7691",
    resolvedSha256: "a87f5f957208d927d693cacf45032ba777d9aeee140ff8488d2f52b6e969d0cb",
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
