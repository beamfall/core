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
const BASE_SERIALIZED_LENGTH = 1_186_073;
const BASE_REGISTRY_SHA256 = "220f2265e5f76041648782e8675191f7180ffc03b4a0d76b74ae701b3643e714";
const BASE_KEY_ORDER_SHA256 = "9544b1e07ef90439c79d6e72e619e5eb1377b50b28c00b059119374653df7cba";
const BASE_PLACEHOLDER_SHA256 = "000e67d0f8544d84602a41eb11a24c13d1a50954b17dbffd03e30a0c7a6136be";
const BASE_CATALOGS = {
  en: {
    partialCount: 3_991,
    partialSha256: "eb4b1e547155b6e1bd97fadbe4ae6e550b7d9ea8134cdc3a1b1748323be7071e",
    resolvedSha256: "10c53d5b44a79859f7c6b10c6f72885e932b0455ae152d885c5b7519278164c7",
  },
  fr: {
    partialCount: 3_991,
    partialSha256: "dc8000e12dd7672fd44e8cc901f4c0d4102aa7b7a01ba11af5b6758a5a497daa",
    resolvedSha256: "48326ef3aa562a2c02e9dd619aa14898ede9c47623a917148b59e37539fa4c6f",
  },
  de: {
    partialCount: 3_991,
    partialSha256: "79d07ddff4dfab539d7b109da6a9f4e44295f2bd7e9b1c967b7578e9bf8bc035",
    resolvedSha256: "9455ba9b97819480d5076bc9af6c563bc1aabcef4962e65806a46fccdab393de",
  },
  es: {
    partialCount: 3_991,
    partialSha256: "c4963c197ab402a8e6ae2bda96f1fe921fc89c44ff69190f28157feec71ed976",
    resolvedSha256: "8a2ae0d3343c9ebefb52c3b7f5e98372841c783d26188b970052baf5dbf8f38f",
  },
  pt: {
    partialCount: 3_991,
    partialSha256: "881532599f0207b3680823675f8aa0754548120cc7f27f5daf642c374425271c",
    resolvedSha256: "e7eda9861d277780878c979cc3bca1eae84e427127263cdb577fd058deb1dcec",
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
