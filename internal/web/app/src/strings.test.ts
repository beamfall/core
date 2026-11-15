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
// merged with the CORE-1119 friendlier admin unlock and promote copy.
const BASE_SERIALIZED_LENGTH = 594_274;
const BASE_REGISTRY_SHA256 = "4efbbe5f72652b9b9956f6a846f7b94f8037768a2475bad794a6946aa4f2f70a";
const BASE_KEY_ORDER_SHA256 = "4789b4e78897a567c1d5cd2596fabe0f586d989b8672768ffb78c1800e2eafe7";
const BASE_PLACEHOLDER_SHA256 = "5c34c6d45f875b8ddb3f5e0fb911f6cd0b9156c37c2f13f54c4fbf118839b376";
const BASE_CATALOGS = {
  en: {
    partialCount: 2_253,
    partialSha256: "67b118cb247f8dd3b803e3bffb3fb6650b75fd689d1d9913ef6b2bceffefe4e6",
    resolvedSha256: "107e58d71b3cefdc299e63d6cdc92673c027e459951a9c5b27dd8ac324b6986d",
  },
  fr: {
    partialCount: 1_073,
    partialSha256: "418f66e1d9e108a43cf895c3e89bd8ad3c2d0dd57de79b03d38056e22101898e",
    resolvedSha256: "9db8b6260423dc1293d23b8111244ca5e3a4b68c161f716d780513dc832d60f1",
  },
  de: {
    partialCount: 955,
    partialSha256: "339bddf2f13c3ef0d8fe81da78e8944947345b7452b772ab1f3e1fe523805765",
    resolvedSha256: "e0d77910b3b0b3cd7241bc3b01e7e8514b70b9198a835bd4e30dc272da44a2ed",
  },
  es: {
    partialCount: 955,
    partialSha256: "d6fba07734bb02c97b25031420df59903da85498ce5b6c91d7c64362d5781102",
    resolvedSha256: "9d765a2b091ca9756a58ac62ca245dd5a4aeb6057134719be2cebcb94764fddf",
  },
  pt: {
    partialCount: 955,
    partialSha256: "28eb32cc624886117858dfe8f00c53728bba0ca1c0a2075bacea061c6c391852",
    resolvedSha256: "da56581149fded542076a3c1bd749a22bcccebb70186958ccb8faa63e76eb9a2",
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
