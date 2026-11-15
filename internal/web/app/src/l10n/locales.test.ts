import { readdirSync, readFileSync } from "node:fs";
import { join, relative, resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { resolveLocalePreference } from "@/lib/i18n";
import {
  buildStringTables,
  PARTIAL_CATALOGS,
  STRINGS,
  type StringKey,
  type StringTable,
} from "@/strings";
import {
  DECLARED_LOCALE_TAGS,
  LOCALE_DECLARATION,
  type LocaleDeclaration,
  type LocaleEntry,
  OFFERED_LOCALE_TAGS,
} from "./locales";

// CORE-2104: the web app derives its locales from the shared declaration. These checks fail when
// the vendored copy drifts from the producer, a declared locale has no catalog (or a catalog is
// undeclared), a shipped locale is missing keys, or a hand-maintained locale list reappears.

const APP_ROOT = process.cwd();
const PRODUCER = resolve(APP_ROOT, "../../../contracts/locales/locales.json");
const VENDORED = resolve(APP_ROOT, "src/l10n/locales.json");
const BCP47 = /^[a-z]{2,3}(-[A-Z][a-z]{3})?(-([A-Z]{2}|\d{3}))?$/;

type Catalogs = Readonly<Record<string, Partial<StringTable>>>;

/** Problems that make a declaration unusable with these catalogs. */
function catalogProblems(declaration: LocaleDeclaration, catalogs: Catalogs): string[] {
  const problems: string[] = [];
  const source = catalogs[declaration.sourceLocale] as Record<string, string> | undefined;
  if (!source) return [`source locale ${declaration.sourceLocale} has no catalog`];
  const sourceKeys = Object.keys(source);
  const declared = new Set(declaration.locales.map((entry) => entry.tag));
  for (const tag of Object.keys(catalogs)) {
    if (!declared.has(tag)) problems.push(`${tag}: catalog is not declared`);
  }
  for (const { tag, status } of declaration.locales) {
    const table = catalogs[tag] as Record<string, string> | undefined;
    if (!table) {
      problems.push(`${tag}: declared but has no catalog`);
      continue;
    }
    const extra = Object.keys(table).filter((key) => !(key in source));
    if (extra.length > 0) problems.push(`${tag}: keys not in the source: ${extra.join(", ")}`);
    if (status !== "shipped") continue;
    const missing = sourceKeys.filter((key) => !table[key]);
    if (missing.length > 0) problems.push(`${tag}: shipped but missing ${missing.length} keys`);
  }
  return problems;
}

const QUOTED_TAG_RUN =
  /(["'`])[a-z]{2,3}(?:-[A-Za-z0-9]{2,8})*\1(?:\s*[,|]\s*(["'`])[a-z]{2,3}(?:-[A-Za-z0-9]{2,8})*\2)+/g;
const BARE_TAG_RUN = /[{[(]\s*[a-z]{2,3}\s*(?:,\s*[a-z]{2,3}\s*){2,},?\s*[}\])]/g;

/** Literal runs (arrays, unions, object shorthands) that name three or more declared locales. */
function hardCodedLocaleLists(source: string, tags: readonly string[]): string[] {
  const declared = new Set(tags.map((tag) => tag.split("-")[0]));
  const hits: string[] = [];
  for (const pattern of [QUOTED_TAG_RUN, BARE_TAG_RUN]) {
    for (const match of source.matchAll(pattern)) {
      const named = new Set(
        (match[0].match(/[a-z]{2,3}(?:-[A-Za-z0-9]{2,8})*/g) ?? [])
          .map((tag) => tag.split("-")[0])
          .filter((primary) => declared.has(primary)),
      );
      if (named.size >= 3) hits.push(match[0]);
    }
  }
  return hits;
}

function sourceFiles(directory: string): string[] {
  return readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    const path = join(directory, entry.name);
    if (entry.isDirectory()) return entry.name === "node_modules" ? [] : sourceFiles(path);
    return /\.(ts|tsx|js|mjs)$/.test(entry.name) ? [path] : [];
  });
}

function entry(tag: string, status: LocaleEntry["status"]): LocaleEntry {
  return { tag, englishName: tag, nativeName: tag, direction: "ltr", status };
}

describe("shared locale declaration", () => {
  it("is a byte-identical copy of contracts/locales/locales.json", () => {
    expect(readFileSync(VENDORED, "utf8")).toBe(readFileSync(PRODUCER, "utf8"));
  });

  it("is well formed", () => {
    expect(LOCALE_DECLARATION.schemaVersion).toBe("1");
    expect(new Set(DECLARED_LOCALE_TAGS).size).toBe(DECLARED_LOCALE_TAGS.length);
    for (const { tag, englishName, nativeName, direction, status } of LOCALE_DECLARATION.locales) {
      expect(tag, tag).toMatch(BCP47);
      expect(englishName && nativeName, tag).toBeTruthy();
      expect(["ltr", "rtl"], tag).toContain(direction);
      expect(["shipped", "in-progress"], tag).toContain(status);
    }
    expect(OFFERED_LOCALE_TAGS).toContain(LOCALE_DECLARATION.sourceLocale);
  });

  it("matches the catalogs: every declared locale built, every shipped locale complete", () => {
    expect(catalogProblems(LOCALE_DECLARATION, PARTIAL_CATALOGS)).toEqual([]);
    expect(Object.keys(STRINGS)).toEqual(DECLARED_LOCALE_TAGS);
  });

  it("flags a shipped locale with missing keys, an undeclared catalog and a missing catalog", () => {
    const catalogs: Catalogs = { en: { a: "A", b: "B" }, fr: { a: "À" }, xx: {} } as never;
    const declaration: LocaleDeclaration = {
      schemaVersion: "1",
      sourceLocale: "en",
      locales: [entry("en", "shipped"), entry("fr", "shipped"), entry("de", "in-progress")],
    };
    expect(catalogProblems(declaration, catalogs)).toEqual([
      "xx: catalog is not declared",
      "fr: shipped but missing 1 keys",
      "de: declared but has no catalog",
    ]);
  });

  it("builds an in-progress locale with per-key source fallback and never offers it", () => {
    const declaration: LocaleDeclaration = {
      schemaVersion: "1",
      sourceLocale: "en",
      locales: [entry("en", "shipped"), entry("fr", "in-progress")],
    };
    const catalogs = { en: { a: "A", b: "B" }, fr: { a: "À" } } as unknown as Catalogs;
    expect(catalogProblems(declaration, catalogs)).toEqual([]);
    expect(buildStringTables(declaration, catalogs)).toEqual({
      en: { a: "A", b: "B" },
      fr: { a: "À", b: "B" },
    });

    const offered = declaration.locales.filter((l) => l.status === "shipped").map((l) => l.tag);
    expect(resolveLocalePreference(["fr-FR"], offered)).toEqual({
      locale: "en",
      displayLocale: "fr-FR",
    });
    const firstKey = Object.keys(STRINGS.en)[0] as StringKey;
    expect(STRINGS.fr[firstKey]).toBeTruthy();
  });
});

describe("no hand-maintained locale lists", () => {
  it("detects arrays, unions and object shorthands that name the declared locales", () => {
    const tags = ["en", "fr", "de", "es", "pt"];
    expect(hardCodedLocaleLists(`const L = ["en", "fr", "de"];`, tags)).toHaveLength(1);
    expect(hardCodedLocaleLists(`type L = "en" | "fr-CA" | 'pt';`, tags)).toHaveLength(1);
    expect(hardCodedLocaleLists(`const all = { en, fr, de, es, pt };`, tags)).toHaveLength(1);
    expect(hardCodedLocaleLists(`["fr", "Volume", "Vitesse"]`, tags)).toEqual([]);
    expect(hardCodedLocaleLists(`["en", "fr"]; languages: ["de"]`, tags)).toEqual([]);
  });

  it("finds none in the app sources", () => {
    const self = resolve(APP_ROOT, "src/l10n/locales.test.ts");
    const files = [
      ...sourceFiles(resolve(APP_ROOT, "src")),
      ...sourceFiles(resolve(APP_ROOT, "e2e")),
      ...readdirSync(APP_ROOT)
        .filter((name) => /\.(ts|mjs|js)$/.test(name))
        .map((name) => resolve(APP_ROOT, name)),
    ].filter((file) => file !== self);
    const hits = files.flatMap((file) =>
      hardCodedLocaleLists(readFileSync(file, "utf8"), DECLARED_LOCALE_TAGS).map(
        (hit) => `${relative(APP_ROOT, file)}: ${hit}`,
      ),
    );
    expect(hits).toEqual([]);
  });
});
