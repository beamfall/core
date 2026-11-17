import { createHash } from "node:crypto";
import { readdirSync, readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { DECLARED_LOCALE_TAGS, LOCALE_DECLARATION } from "./l10n/locales";
import {
  type Locale,
  PARTIAL_CATALOGS,
  STRINGS,
  type StringKey,
  type StringTable,
} from "./strings";
import { en } from "./strings/catalog";

// The locale set comes from the shared declaration (CORE-2104), never a hand-written list.
const LOCALES = DECLARED_LOCALE_TAGS as readonly Locale[];
const SHIPPED = new Set(
  LOCALE_DECLARATION.locales.filter((entry) => entry.status === "shipped").map((l) => l.tag),
);

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
// merged with the CORE-1877 add-to-playlist library copy;
// merged with the CORE-1614 Add a device QR copy;
// updated for the CORE-1853 bitrate-ceiling transcode reason copy;
// merged with the CORE-1717 remote cap and CORE-1742 offline pack stack copy;
// merged with the CORE-1620 signed-out Console device copy;
// merged with the CORE-1970 transcoder resource settings and HDR tone-map disabled copy;
// updated for the CORE-2077 Library folders, Add folder and file tools copy.
// updated for the CORE-1237 one-key plural search result count, the CORE-1208 localized activity
// log copy and the CORE-1204/1223/1255/1271/1310/2052 completion of the fr, de, es and pt catalogs.
// merged with the CORE-0557 spoken scrubber time copy.
// merged with the CORE-2109 Console job purpose, target and Scan now copy.
// merged with the CORE-2011/2012/2013/0292 plain-language Console, player and scan copy.
// merged with the CORE-1118/1119/1121/1123/1125/1179 surface leads and Appearance copy;
// merged with the CORE-2120 Live sessions end-session copy;
// updated for the CORE-0043 unsupported-codec copy, which no longer names a Profile setting.
// updated for the CORE-2175 backup key copy: the before-confirming note and the days-left count
// are gone, since confirming keeps the key every set was sealed to;
// merged with the CORE-2181 transcode-disallowed playback details reason copy;
// merged with the CORE-1635 disc grouping copy;
// merged with the CORE-2190 generic plugin key copy and the CORE-2040 scan summary copy.
// merged with the CORE-0317 and CORE-2064 device pairing request copy and
// the CORE-2070 Ask to join copy.
// merged with the pairing request review copy: the separate manager approval, the
// announcement, the plural queue count, and the link wording removed.
// merged with the CORE-2245 lead and follower screen copy.
// merged with the CORE-2303 session length copy.
// merged with the CORE-2308 job queue, CORE-2311 optional provider key and CORE-2310/2309
// Overview attention and first-value copy.
const BASE_SERIALIZED_LENGTH = 766_917;
const BASE_REGISTRY_SHA256 = "463733498b66f57b92dba6e7e571a7190612a0f599a0304f224e11ec1e644f1a";
const BASE_KEY_ORDER_SHA256 = "5535c4ecf5b65e5f12788026000e9c7f618a256f56ffe6e10a7ec026682ed959";
const BASE_PLACEHOLDER_SHA256 = "4efb49f7a29ef2560d4c14173a1bc11ef3bf6de64ea4664ec971922f53d688b8";
const BASE_CATALOGS = {
  en: {
    partialCount: 2_635,
    partialSha256: "cd74f2f649765359ebdc1a422e19f008753553b6326ced99c643b48fe6effb83",
    resolvedSha256: "0e0fface8a7576cc77a4f00fdd5e8911514fe284534315725338951ee304e989",
  },
  fr: {
    partialCount: 2_635,
    partialSha256: "5308187f028f0e93bf5a2ac987cb20ddb5803e611e04938071fecc46a259e4e4",
    resolvedSha256: "2131408fa3f0d9c3b167386c1032e973fb6df4c12cb7f36ee153667cac728a09",
  },
  de: {
    partialCount: 2_635,
    partialSha256: "4245b7975ae01a75efd198f627d35b3fbd28784c47658e6f05f92c65d238f62a",
    resolvedSha256: "52a701304683e1f9ce2cc93626d744fa34a77fd9c565699b57e18333dc67fe28",
  },
  es: {
    partialCount: 2_635,
    partialSha256: "eb207a1475e0d248157dbc2aa4e241aad39ee1d0246ad701d03dc7c20a361ada",
    resolvedSha256: "8292a9419a1471e981b8b93458c57d026713490541c910fb18d25f215d6501cd",
  },
  pt: {
    partialCount: 2_635,
    partialSha256: "2518d10cef1ddf0d6993b1f2d39c78f63f46d2803685e63a71bcfe4d37800fba",
    resolvedSha256: "bd4a89f410185815f57b79e6011bc9764d42f676f66291f95f4282d435449c95",
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

const BRANCHING = new Set(["plural", "select", "selectordinal"]);

/**
 * The ICU-lite arguments of a message, in order (CORE-1237). A plural or select argument is
 * `name:type[...]` with the sorted, de-duplicated arguments of its branches, so translated branch
 * text and a locale's extra plural categories never count as arguments.
 */
function messageArguments(message: string): string[] {
  const names: string[] = [];
  let i = 0;
  // Like formatMessage: apostrophe quoting applies only once a message uses a typed argument.
  const icu = /\{[^{}]*,/.test(message);
  const skipQuoted = (): boolean => {
    if (!icu || message[i] !== "'") return false;
    const close = message[i + 1] === "'" ? i + 1 : message.indexOf("'", i + 1);
    i = close < 0 ? message.length : close + 1;
    return true;
  };
  const text = (nested: boolean, into: string[]): void => {
    while (i < message.length && !(nested && message[i] === "}")) {
      if (skipQuoted()) continue;
      if (message[i] === "{") argument(into);
      else i++;
    }
  };
  const argument = (into: string[]): void => {
    const head = /^\{\s*([A-Za-z_]\w*)\s*(?:,\s*(\w+)\s*)?([,}])/.exec(message.slice(i));
    if (!head) {
      i++;
      return;
    }
    i += head[0].length;
    const [, name, type, delimiter] = head;
    if (!type || !BRANCHING.has(type) || delimiter !== ",") {
      into.push(name);
      if (delimiter === ",") {
        text(true, []);
        i++;
      }
      return;
    }
    const branches: string[] = [];
    for (;;) {
      const selector = /^\s*(?:offset:\d+\s+)?(=?[\w-]+)?\s*/.exec(message.slice(i));
      i += selector?.[0].length ?? 0;
      if (message[i] !== "{" || !selector?.[1]) break;
      i++;
      text(true, branches);
      i++;
    }
    if (message[i] === "}") i++;
    else branches.push("<malformed>");
    into.push(`${name}:${type}[${[...new Set(branches)].sort().join(",")}]`);
  };
  text(false, names);
  return names;
}

function placeholderInventory(table: StringTable): Record<string, string[]> {
  const inventory: Record<string, string[]> = {};
  for (const [key, value] of Object.entries(table)) {
    const placeholders = messageArguments(value);
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

  it("names a Profile setting only in the transcode-disallowed copy (CORE-0043)", () => {
    // The unsupported-codec message covers a browser that cannot decode the file, whatever the
    // Profile allows; only the server's transcode-disallowed refusal may point at the setting.
    const profileWord = /profile|profil|perfil/i;
    for (const locale of LOCALES) {
      expect(STRINGS[locale].playbackUnsupportedCodec, locale).not.toMatch(profileWord);
      expect(STRINGS[locale].playbackUnsupportedCodec, locale).not.toMatch(/conver|konvert/i);
      expect(STRINGS[locale].playbackTranscodeDisallowed, locale).toMatch(profileWord);
    }
  });

  it("keeps English and every shipped locale complete; others resolve through English", () => {
    const englishKeys = Object.keys(en) as StringKey[];

    expect(Object.values(en).every((value) => typeof value === "string" && value.length > 0)).toBe(
      true,
    );
    for (const locale of LOCALES) {
      const partial = PARTIAL_CATALOGS[locale];
      expect(STRINGS[locale]).toEqual(withEnglishFallback(partial));

      const missingKeys = englishKeys.filter((key) => !partial[key]);
      if (SHIPPED.has(locale)) expect(missingKeys, `${locale} missing keys`).toEqual([]);
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

describe("placeholder guard (CORE-1237)", () => {
  const source = "{count, plural, one {# result for “{query}”} other {# results for “{query}”}}";

  it("accepts an ICU plural with translated branches and extra plural categories", () => {
    const expected = messageArguments(source);
    expect(expected).toEqual(["count:plural[query]"]);
    expect(
      messageArguments(
        "{count, plural, one {# résultat pour « {query} »} other {# résultats pour « {query} »}}",
      ),
    ).toEqual(expected);
    expect(
      messageArguments(
        "{count, plural, one {{query}: # wynik} few {{query}: # wyniki} many {{query}: # wyników} other {{query}: # wyniku}}",
      ),
    ).toEqual(expected);
    expect(messageArguments("{count, plural, one {item} other {items}}")).toEqual([
      "count:plural[]",
    ]);
    expect(messageArguments("{n, number} '{'{name}'}' {name}")).toEqual(["n", "name", "name"]);
  });

  it("rejects renamed, dropped, extra or retyped arguments and broken plurals", () => {
    const expected = messageArguments(source);
    for (const broken of [
      "{count, plural, one {# résultat pour « {requete} »} other {# résultats}}",
      "{count, plural, one {# résultat} other {# résultats}}",
      "{count, plural, one {# résultat pour « {query} » {name}} other {# résultats pour « {query} »}}",
      "{count} résultats pour « {query} »",
      "{total, plural, one {# résultat pour « {query} »} other {# résultats pour « {query} »}}",
      "{count, plural, one {# résultat pour « {query} »} other # résultats pour « {query} »}",
    ]) {
      expect(messageArguments(broken), broken).not.toEqual(expected);
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
