// Localized UI strings (ADR-0034). Ported in full from
// internal/web/static/strings.js. `en` is the complete key set and the
// fallback; other locales may be partial.
//
// Contract: keys map to plain strings (optionally with {var} placeholders).
// Never embed HTML — strings render as text (ADR-0012 §5).

import { de, en, es, fr, pt } from "./strings/catalog";

export type StringKey = keyof typeof en;
export type StringTable = Record<StringKey, string>;
export type Locale = "en" | "fr" | "de" | "es" | "pt";

export const STRINGS: Record<Locale, Partial<StringTable> & StringTable> = {
  en,
  fr: { ...en, ...fr },
  de: { ...en, ...de },
  es: { ...en, ...es },
  pt: { ...en, ...pt },
};
