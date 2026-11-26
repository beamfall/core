import { createHash } from "node:crypto";
import { readFileSync, writeFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
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

// Ratchet baselines of the current catalogs; this file's git log records which change moved them.
// After a catalog change (or a merge conflict in these constants: take either side), run
// `npm run strings:rerecord` (CORE-2455). It rewrites only the BASE_* constants below from the
// current catalogs, then runs this file.
const BASE_SERIALIZED_LENGTH = 1_186_278;
const BASE_REGISTRY_SHA256 = "29fc8d3d40a1c76adde76c617437df07a74d6eb341b9f9ab5334452d031addb6";
const BASE_KEY_ORDER_SHA256 = "b08789e1668e16c85ff4f13e216171b0e41f195fc8650ce1246f548925a74f6e";
const BASE_PLACEHOLDER_SHA256 = "e55ef8f2a0a3e79cf98c9e35fe910923a5c4eddf1809c8674bb73e137ca7f72e";
const BASE_CATALOGS = {
  en: {
    partialCount: 3_990,
    partialSha256: "5f5aecfe89f6c4f2652c20075a8d50e79514e3678942552cf7e80d977679a74a",
    resolvedSha256: "4b61d250d65165b58c111f5e1d9fc804cc826bb5e057f52f872e1478895a99e4",
  },
  fr: {
    partialCount: 3_990,
    partialSha256: "4f115bcf73f93fcb9cb4e1b9c5a8f9e157eb9cab31c28a6d991fb608eb1e8a83",
    resolvedSha256: "86f3f694b32bb558bf2f1945a06c749f13a82caf289b1ac94664716cc5df9168",
  },
  de: {
    partialCount: 3_990,
    partialSha256: "15ca18e7bb5a86d75d8eb30f76c074a3098ff051fb2b337e670d2dc7dac77e16",
    resolvedSha256: "50fd9e1e63902a0e008cdb830024a19bb9eff632c29ca743e744c8e3f3d5558c",
  },
  es: {
    partialCount: 3_990,
    partialSha256: "823159c32786b04d1c1d9a091e0a44e4ec42b6af3728992f7398a7402580d849",
    resolvedSha256: "bc1328c102720214284ea62ca37e5071105dc5305ccb0e90f26a945172e20bbd",
  },
  pt: {
    partialCount: 3_990,
    partialSha256: "63f28b020526ce086069f471d0b343f8d235e6e35072483ffbbfabd8fdecf903",
    resolvedSha256: "80c39b310c894f8bdd59fd238673135792ea9837ef90c3d7a4db38b5849c1173",
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

interface Baselines {
  serializedLength: number;
  registrySha256: string;
  keyOrderSha256: string;
  placeholderSha256: string;
  catalogs: Record<Locale, { partialCount: number; partialSha256: string; resolvedSha256: string }>;
}

/** The ratchet values the current catalogs produce, as the BASE_* constants record them. */
function currentBaselines(): Baselines {
  const serialized = JSON.stringify(STRINGS);
  return {
    serializedLength: serialized.length,
    registrySha256: sha256(serialized),
    keyOrderSha256: sha256(JSON.stringify(Object.keys(en))),
    placeholderSha256: sha256(JSON.stringify(placeholderInventory(STRINGS.en))),
    catalogs: Object.fromEntries(
      LOCALES.map((locale) => [
        locale,
        {
          partialCount: Object.keys(PARTIAL_CATALOGS[locale]).length,
          partialSha256: sha256(sortedSerialization(PARTIAL_CATALOGS[locale])),
          resolvedSha256: sha256(JSON.stringify(STRINGS[locale])),
        },
      ]),
    ) as Baselines["catalogs"],
  };
}

function digitGroups(value: number): string {
  return String(value).replace(/\B(?=(\d{3})+(?!\d))/g, "_");
}

/** This file's source with its BASE_* constants rewritten to `values`; nothing else changes. */
function rerecordBaselines(source: string, values: Baselines): string {
  // Keep the checkout's own line endings.
  const eol = source.includes("\r\n") ? "\r\n" : "\n";
  const catalogs = LOCALES.map((locale) => {
    const entry = values.catalogs[locale];
    // A regional tag such as fr-CA is not a bare property name.
    const name = /^[A-Za-z_$][\w$]*$/.test(locale) ? locale : JSON.stringify(locale);
    return [
      `  ${name}: {`,
      `    partialCount: ${digitGroups(entry.partialCount)},`,
      `    partialSha256: "${entry.partialSha256}",`,
      `    resolvedSha256: "${entry.resolvedSha256}",`,
      "  },",
    ].join(eol);
  }).join(eol);
  const replacements: [RegExp, string][] = [
    [
      /^const BASE_SERIALIZED_LENGTH = [\d_]+;(?=\r?$)/m,
      `const BASE_SERIALIZED_LENGTH = ${digitGroups(values.serializedLength)};`,
    ],
    [
      /^const BASE_REGISTRY_SHA256 = "[0-9a-f]+";(?=\r?$)/m,
      `const BASE_REGISTRY_SHA256 = "${values.registrySha256}";`,
    ],
    [
      /^const BASE_KEY_ORDER_SHA256 = "[0-9a-f]+";(?=\r?$)/m,
      `const BASE_KEY_ORDER_SHA256 = "${values.keyOrderSha256}";`,
    ],
    [
      /^const BASE_PLACEHOLDER_SHA256 = "[0-9a-f]+";(?=\r?$)/m,
      `const BASE_PLACEHOLDER_SHA256 = "${values.placeholderSha256}";`,
    ],
    [
      /^const BASE_CATALOGS = \{\r?\n[\s\S]*?\r?\n\} as const satisfies/m,
      `const BASE_CATALOGS = {${eol}${catalogs}${eol}} as const satisfies`,
    ],
  ];
  let next = source;
  for (const [pattern, replacement] of replacements) {
    if (!pattern.test(next)) throw new Error(`strings.test.ts: no match for ${pattern}`);
    next = next.replace(pattern, () => replacement);
  }
  return next;
}

describe("strings baseline re-record (CORE-2455)", () => {
  // Runs only under `npm run strings:rerecord`; a plain test run never rewrites anything.
  it.runIf(process.env.STRINGS_RERECORD === "1")("rerecords the BASE_* constants", () => {
    const path = fileURLToPath(import.meta.url);
    writeFileSync(path, rerecordBaselines(readFileSync(path, "utf8"), currentBaselines()));
  });

  it("rewrites only the constants, and the recorded file matches the current catalogs", () => {
    const source = readFileSync(fileURLToPath(import.meta.url), "utf8");
    const values = currentBaselines();
    const rewritten = rerecordBaselines(source, values);
    // The committed file already holds the current values, so a re-record is a no-op.
    expect(rewritten).toBe(source);
    const changed = rerecordBaselines(source, { ...values, registrySha256: "0".repeat(64) });
    expect(changed).not.toBe(source);
    expect(changed.replace("0".repeat(64), values.registrySha256)).toBe(source);
    // A CRLF checkout re-records in place too.
    const crlf = source.replace(/\n/g, "\r\n");
    expect(rerecordBaselines(crlf, values)).toBe(crlf);
  });
});

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
