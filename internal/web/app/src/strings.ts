// Localized UI strings (ADR-0034). Ported in full from
// internal/web/static/strings.js. The source locale's catalog is the complete key set and the
// per-key fallback; which locales are built comes from the shared declaration (CORE-2104).
//
// Contract: keys map to plain strings (optionally with {var} placeholders).
// Never embed HTML — strings render as text (ADR-0012 §5).

import { LOCALE_DECLARATION, type LocaleDeclaration } from "./l10n/locales";
import * as catalogs from "./strings/catalog";

export type StringKey = keyof typeof catalogs.en;
export type StringTable = Record<StringKey, string>;
/** A locale with a catalog in `src/strings/catalog.ts`. */
export type Locale = keyof typeof catalogs;

export const PARTIAL_CATALOGS: Record<Locale, Partial<StringTable>> = catalogs;

export function isCatalogLocale(tag: string): tag is Locale {
  return Object.hasOwn(catalogs, tag);
}

/** One resolved table per declared locale that has a catalog, in declaration order. A key the
 *  locale's catalog lacks resolves to the source locale's text. */
export function buildStringTables(
  declaration: LocaleDeclaration,
  partials: Readonly<Record<string, Partial<StringTable>>>,
): Record<string, StringTable> {
  const source = partials[declaration.sourceLocale] as StringTable;
  return Object.fromEntries(
    declaration.locales
      .filter((entry) => Object.hasOwn(partials, entry.tag))
      .map(({ tag }) => [tag, { ...source, ...partials[tag] }]),
  );
}

export const STRINGS = buildStringTables(LOCALE_DECLARATION, PARTIAL_CATALOGS) as Record<
  Locale,
  StringTable
>;
