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
// CORE-1363 player recovery copy, the CORE-1342 source-ends-early copy and the CORE-1117 copy cleanup
// and orphan-key removal; updated for the setup/enroll redesign copy tightening and removal of
// the orphaned setupFreshServer and setupPlugin* keys; merged with the Normal/Advanced settings copy and the setup and pairing error copy, dropping the orphaned pairingCodeManagerOnly, enrollmentFailed and promotionFailed keys; merged with
// the CORE-1132 first admin password copy and the CORE-0393 Viewer pairing-code switch copy;
// merged with the CORE-1126 metadata credits copy, the CORE-1171 delete-library copy and the CORE-1117 design-system
// copy (settings modes, onboarding, viewer browse and detail); merged with the CORE-1200 Library icon
// and setup-checklist Optional copy; merged with the CORE-1201 library restore copy; merged with
// the CORE-1144 media detail copy; merged with the CORE-1156 add-library copy; merged with the CORE-1606 library folder health copy
// and the CORE-1598 to CORE-1610 onboarding copy (setup folder errors, password rule, enroll and
// add-a-device hints); updated for the CORE-1604 saved plugin choices retry copy and the CORE-1607
// manual copy-link field copy; merged with the CORE-1681 subtitle appearance copy;
// merged with the CORE-1659 karaoke key and tempo copy;
// merged with the CORE-1671 subtitle offset and audio delay copy, the CORE-1682 player keyboard
// shortcut sheet copy and the CORE-1683 player chapter list copy;
// merged with the CORE-1689 "still watching" prompt and setting copy;
// merged with the CORE-1678 remote control and Play on copy;
// merged with the CORE-1676 Version picker copy;
// merged with the CORE-1701 pre-play copy;
// merged with the CORE-1685 library letter rail copy;
// merged with the CORE-1697 webhook copy;
// updated for the CORE-1540 disabled provider-key copy;
// merged with the CORE-1785 media detail lockup copy;
// merged with the CORE-1764 Watch together copy;
// merged with the CORE-1786 container entity copy;
// merged with the CORE-1684 viewer Collections copy;
// merged with the CORE-1679 Cast sender copy;
// merged with the CORE-1718 skip link, CORE-1633 mini player, CORE-1635
// listening browse and CORE-1709 track menu copy;
// merged with the CORE-1715 item metadata editor copy;
// merged with the CORE-1534 Libraries provider-key copy;
// merged with the CORE-1716 console server-log copy;
// merged with the CORE-1720 console staged restore copy and the reworded backup restore safety line;
// merged with the CORE-1717 remote bitrate cap copy;
// merged with the CORE-1179 appearance (color scheme and theme preset) copy.
const BASE_SERIALIZED_LENGTH = 596_879;
const BASE_REGISTRY_SHA256 = "427554ccd2fcf5b7661f5c6ec38fa0c8450b2d580ac3bb80857b22d67b435304";
const BASE_KEY_ORDER_SHA256 = "eeb6cab1605b7c518b286b5a84983154b4666ad080fcf07ad2d35cc61b0c33e3";
const BASE_PLACEHOLDER_SHA256 = "5c34c6d45f875b8ddb3f5e0fb911f6cd0b9156c37c2f13f54c4fbf118839b376";
const BASE_CATALOGS = {
  en: {
    partialCount: 2_267,
    partialSha256: "a3896f2eef9bc840a2b8a6b477fbb228f031fe16e564698008204ef4b86b3cf8",
    resolvedSha256: "405510013f90ec2d2ef2178762abe7b85549e02675d77571238a7652de026d64",
  },
  fr: {
    partialCount: 1_087,
    partialSha256: "819a61a7b91f8cd7ba9bd89955698a89274a93b35396576b3d9380edc4313741",
    resolvedSha256: "04b7b152fc766c2b14a8c26855cc3460ef31a4661eae35a500183c6b7adc447a",
  },
  de: {
    partialCount: 969,
    partialSha256: "1be9b441b7cfcced832ad8b1b997ee51b97c2e7458a9b9f8d1eba778c08f908c",
    resolvedSha256: "206365aeae0f663f4b4c4fb626dbe393c88d2f2cc09fa81e59bbc63d61bd402c",
  },
  es: {
    partialCount: 969,
    partialSha256: "05b2a13bd0832999bceec282e52720aeb43fdc21dce28a537eca60a148d92fef",
    resolvedSha256: "9574d89bd3244e614a2a0f1dfb1bbe8daa1dd6dc878d5bfb316a286e5ecb2823",
  },
  pt: {
    partialCount: 969,
    partialSha256: "57b3e36abfc5f201b4f1d13ad0c69607dc8afa0694164216abf36c932cddf0a5",
    resolvedSha256: "9fe57f21e8f21368d1dcd43edfc6791005ec6f1657406e1cd79ddc78e889ef83",
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
