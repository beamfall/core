// Shared locale declaration (CORE-2104). `locales.json` is a byte-identical copy of the producer
// file `contracts/locales/locales.json` at the repository root; `locales.test.ts` enforces the copy,
// catalog parity and the absence of hand-maintained locale lists. Add a language by declaring it
// there, copying the file here and adding its catalog to `src/strings/catalog.ts`.
import declaration from "./locales.json";

/** `shipped`: offered to users and complete. `in-progress`: built and key-parity checked, never
 *  offered; missing keys resolve per key to the source locale. */
export type LocaleStatus = "shipped" | "in-progress";

export interface LocaleEntry {
  tag: string;
  englishName: string;
  nativeName: string;
  direction: "ltr" | "rtl";
  status: LocaleStatus;
}

export interface LocaleDeclaration {
  schemaVersion: string;
  sourceLocale: string;
  locales: readonly LocaleEntry[];
}

export const LOCALE_DECLARATION = declaration as LocaleDeclaration;
export const SOURCE_LOCALE = LOCALE_DECLARATION.sourceLocale;

/** Every declared locale, in declaration order: these are built. */
export const DECLARED_LOCALE_TAGS: readonly string[] = LOCALE_DECLARATION.locales.map(
  (entry) => entry.tag,
);

/** The locales users may be given: shipped ones only. */
export const OFFERED_LOCALE_TAGS: readonly string[] = LOCALE_DECLARATION.locales
  .filter((entry) => entry.status === "shipped")
  .map((entry) => entry.tag);
